package infra

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/redis/go-redis/v9"
)

// CapacityConfig bounds waiting plus executing requests in this process.
// Supplier quotas alone are shared in Redis. KeyPrefix defaults to
// llm:{capacity}:v2; all coordination keys share one Redis Cluster slot.
type CapacityConfig struct {
	MaxInFlight   int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	KeyPrefix     string
}

// CapacityScheduler admits locally without waiting for a process slot, then
// waits for shared supplier pool/model capacity in the caller's context.
// No distributed queue or workload priority is maintained. Each admission
// observes monotonically versioned supply settings; revision keys deliberately
// survive idle periods so stale registry snapshots cannot restore older limits.
//
// Each identity has a renewable lease and a second full-lease reclaim grace.
// The local watchdog cancels upstream context before a slot is reclaimable.
// Caller cancellation, shutdown, and renewal failure only stop renewal; they
// do not free the active identity. Only actual attempt completion may Release.
// Neither cancellation nor a crashed/paused process proves that a remote
// provider has stopped: callers must propagate Context, close upstream bodies,
// and accept this unavoidable distributed cancellation boundary.
//
// The supplied client should use finite network timeouts, disabled automatic
// retries, and ContextTimeoutEnabled. The scheduler bounds its own waits even
// if a client's I/O does not honor cancellation; uncertain admissions are
// abandoned by identity, not retried as fresh supplier requests.
type CapacityScheduler struct {
	client  redis.UniversalClient
	config  CapacityConfig
	keys    []string
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelCauseFunc
	mu      sync.Mutex
	permits map[string]*capacityPermit
}

var _ domain.CapacityScheduler = (*CapacityScheduler)(nil)

func NewCapacityScheduler(client redis.UniversalClient, config CapacityConfig) (*CapacityScheduler, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: Redis client is required", domain.ErrCapacityInvalidRequest)
	}
	if config.MaxInFlight <= 0 {
		return nil, fmt.Errorf("%w: max in-flight protection must be positive", domain.ErrCapacityInvalidRequest)
	}
	if config.LeaseDuration < 30*time.Millisecond || config.PollInterval <= 0 || config.PollInterval > config.LeaseDuration/3 {
		return nil, fmt.Errorf("%w: lease must be at least 30ms and polling at most one third of the lease", domain.ErrCapacityInvalidRequest)
	}
	if config.KeyPrefix == "" {
		config.KeyPrefix = "llm:{capacity}:v2"
	}
	if start := strings.IndexByte(config.KeyPrefix, '{'); start >= 0 {
		if end := strings.IndexByte(config.KeyPrefix[start+1:], '}'); end <= 0 {
			return nil, fmt.Errorf("%w: key prefix has an invalid Redis hash tag", domain.ErrCapacityInvalidRequest)
		}
	} else {
		config.KeyPrefix += ":{capacity}"
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &CapacityScheduler{client: client, config: config, ctx: ctx, cancel: cancel, permits: make(map[string]*capacityPermit), slots: make(chan struct{}, config.MaxInFlight)}
	for _, suffix := range []string{"active", "permits", "counts"} {
		s.keys = append(s.keys, config.KeyPrefix+":"+suffix)
	}
	return s, nil
}

type capacityRequest struct {
	Pool         string `json:"p"`
	Model        string `json:"m"`
	Tokens       int    `json:"t"`
	PoolVersion  string `json:"pv"`
	ModelVersion string `json:"mv"`
	PoolLimits   [4]int `json:"pl"`
	ModelLimits  [4]int `json:"ml"`
}

func capacityScope(kind, id string) string {
	sum := sha256.Sum256([]byte(id))
	return kind + hex.EncodeToString(sum[:])
}

func capacityLimits(limits domain.CapacityLimits) ([4]int, error) {
	values := [4]int{limits.RequestsPerMinute, limits.RequestsPerSecond, limits.TokensPerSecond, limits.Concurrency}
	for _, value := range values {
		if value < 0 || uint64(value) > 1<<53-1 {
			return values, fmt.Errorf("%w: limits must be nonnegative exact Redis integers", domain.ErrCapacityInvalidRequest)
		}
	}
	return values, nil
}

func prepareCapacityRequest(request domain.AdmissionRequest) (capacityRequest, error) {
	model := request.Model
	if model == nil || model.ID == "" || model.ConnectionID == "" || model.QuotaPoolID == "" {
		return capacityRequest{}, fmt.Errorf("%w: a resolved model, connection and quota pool are required", domain.ErrCapacityInvalidRequest)
	}
	if request.InputTokens < 0 || uint64(request.InputTokens) > 1<<53-1 || model.PoolRevision < 0 || model.ModelRevision < 0 {
		return capacityRequest{}, fmt.Errorf("%w: token estimates and revisions must be nonnegative", domain.ErrCapacityInvalidRequest)
	}
	pool, err := capacityLimits(model.PoolLimits)
	if err != nil {
		return capacityRequest{}, err
	}
	modelLimits, err := capacityLimits(model.ModelLimits)
	if err != nil {
		return capacityRequest{}, err
	}
	for _, values := range [][4]int{pool, modelLimits} {
		if values[2] > 0 && (request.InputTokens <= 0 || request.InputTokens > values[2]) {
			return capacityRequest{}, fmt.Errorf("%w: token estimate must fit each configured one-second token budget", domain.ErrCapacityInvalidRequest)
		}
	}
	return capacityRequest{
		Pool: capacityScope("p", model.QuotaPoolID), Model: capacityScope("m", model.ID),
		Tokens:      request.InputTokens,
		PoolVersion: fmt.Sprintf("%020d", model.PoolRevision), ModelVersion: fmt.Sprintf("%020d", model.ModelRevision),
		PoolLimits: pool, ModelLimits: modelLimits,
	}, nil
}

func (s *CapacityScheduler) command(ctx context.Context, operation, id, payload string) (int64, error) {
	return capacityScript.Run(ctx, s.client, s.keys, operation, id, s.config.LeaseDuration.Milliseconds(), s.config.KeyPrefix, payload).Int64()
}

type capacityResult struct {
	code    int64
	err     error
	started time.Time
}

// run bounds the caller even when the externally provided Redis client's
// socket configuration ignores context deadlines. Its result channel is
// buffered so an eventually completed I/O operation cannot leak a sender.
func (s *CapacityScheduler) run(ctx context.Context, operation, id, payload string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.LeaseDuration/3)
	defer cancel()
	result := make(chan capacityResult, 1)
	go func() {
		code, err := s.command(ctx, operation, id, payload)
		result <- capacityResult{code: code, err: err}
	}()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case value := <-result:
		return value.code, value.err
	}
}

func (s *CapacityScheduler) cleanup(id string, abandoned bool) {
	ctx, cancel := context.WithTimeout(context.Background(), min(time.Second, s.config.LeaseDuration/3))
	defer cancel()
	operation := "release"
	if abandoned {
		operation = "abandon"
	}
	// On failure, identity-based lease reclamation remains the fallback. A
	// cleanup never deletes another request's permit or decrements its count.
	_, _ = s.run(ctx, operation, id, "")
}

func (s *CapacityScheduler) Acquire(ctx context.Context, request domain.AdmissionRequest) (domain.CapacityPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.ctx.Err() != nil {
		return nil, domain.ErrCapacityClosed
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, domain.ErrCapacityBusy
	}
	transferred := false
	defer func() {
		if !transferred {
			<-s.slots
		}
	}()
	prepared, err := prepareCapacityRequest(request)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(prepared)
	if err != nil {
		return nil, fmt.Errorf("%w: encode admission: %v", domain.ErrCapacityInvalidRequest, err)
	}
	workCtx, cancel := context.WithCancelCause(ctx)
	stopClose := context.AfterFunc(s.ctx, func() { cancel(domain.ErrCapacityClosed) })
	id := uuid.NewString()
	cleanupRequired := false
	defer func() {
		if !transferred {
			cancel(context.Canceled)
			stopClose()
			if cleanupRequired {
				s.cleanup(id, true)
			}
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	payloadString := string(payload)
	for {
		select {
		case <-workCtx.Done():
			return nil, context.Cause(workCtx)
		case <-timer.C:
		}
		started := time.Now()
		cleanupRequired = true
		code, err := s.run(workCtx, "acquire", id, payloadString)
		if workCtx.Err() != nil {
			return nil, context.Cause(workCtx)
		}
		// Socket deadlines can fire before the context timer goroutine publishes
		// Err. Classify only an elapsed caller deadline as caller expiry; an
		// earlier coordination timeout must still fail closed as unavailable.
		if deadline, ok := workCtx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
		if err != nil {
			return nil, fmt.Errorf("%w: admission: %v", domain.ErrCapacityUnavailable, err)
		}
		switch code {
		case 1:
			deadline := started.Add(s.config.LeaseDuration)
			if !time.Now().Before(deadline) {
				return nil, domain.ErrCapacityLeaseLost
			}
			permit := &capacityPermit{scheduler: s, id: id, ctx: workCtx, cancel: cancel, stopClose: stopClose}
			s.mu.Lock()
			if s.ctx.Err() != nil || workCtx.Err() != nil {
				s.mu.Unlock()
				if workCtx.Err() != nil {
					return nil, context.Cause(workCtx)
				}
				return nil, domain.ErrCapacityClosed
			}
			s.permits[id] = permit
			s.mu.Unlock()
			transferred = true
			go permit.guard(deadline)
			return permit, nil
		case 0:
			timer.Reset(s.config.PollInterval)
		case -2:
			return nil, domain.ErrCapacityLeaseLost
		case -4:
			cleanupRequired = false // No admission was committed for an impossible estimate.
			return nil, fmt.Errorf("%w: token estimate no longer fits current supplier limits", domain.ErrCapacityInvalidRequest)
		default:
			return nil, fmt.Errorf("%w: admission state %d", domain.ErrCapacityUnavailable, code)
		}
	}
}

func (s *CapacityScheduler) Close() error {
	s.cancel(domain.ErrCapacityClosed)
	s.mu.Lock()
	for _, permit := range s.permits {
		permit.cancel(domain.ErrCapacityClosed)
		permit.stopClose()
	}
	// Shutdown is not evidence that upstream calls have terminated. Their
	// owners may still Release after closing I/O; otherwise leases recover.
	clear(s.permits)
	s.mu.Unlock()
	return nil
}

type capacityPermit struct {
	scheduler *CapacityScheduler
	id        string
	ctx       context.Context
	cancel    context.CancelCauseFunc
	stopClose func() bool
	once      sync.Once
}

func (p *capacityPermit) Context() context.Context { return p.ctx }

func (p *capacityPermit) Release() {
	p.finish(false)
}

func (p *capacityPermit) Abort() {
	p.finish(true)
}

func (p *capacityPermit) finish(abandoned bool) {
	p.once.Do(func() {
		// Cancellation precedes either normal release or undispatched refund.
		p.cancel(context.Canceled)
		p.stopClose()
		p.scheduler.cleanup(p.id, abandoned)
		p.scheduler.mu.Lock()
		delete(p.scheduler.permits, p.id)
		p.scheduler.mu.Unlock()
		<-p.scheduler.slots
	})
}

func (p *capacityPermit) guard(deadline time.Time) {
	watchdog := time.NewTimer(time.Until(deadline))
	defer watchdog.Stop()
	renew := time.NewTimer(max(0, time.Until(deadline.Add(-2*p.scheduler.config.LeaseDuration/3))))
	defer renew.Stop()
	var result <-chan capacityResult
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-watchdog.C:
			p.cancel(domain.ErrCapacityLeaseLost)
			return
		case <-renew.C:
			response := make(chan capacityResult, 1)
			result = response
			started := time.Now()
			ctx, cancel := context.WithDeadline(p.ctx, minTime(deadline, started.Add(p.scheduler.config.LeaseDuration/3)))
			go func() {
				defer cancel()
				code, err := p.scheduler.command(ctx, "renew", p.id, "")
				response <- capacityResult{code: code, err: err, started: started}
			}()
		case value := <-result:
			result = nil
			if value.err != nil || value.code != 1 || !time.Now().Before(deadline) {
				cause := domain.ErrCapacityLeaseLost
				if value.err != nil {
					cause = fmt.Errorf("%w: renewal: %v", domain.ErrCapacityUnavailable, value.err)
				}
				p.cancel(cause)
				return
			}
			deadline = value.started.Add(p.scheduler.config.LeaseDuration)
			watchdog.Reset(time.Until(deadline))
			renew.Reset(max(0, time.Until(value.started.Add(p.scheduler.config.LeaseDuration/3))))
		}
	}
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
