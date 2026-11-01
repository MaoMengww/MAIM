package infra_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/infra"
	"github.com/redis/go-redis/v9"
)

func capacityRedis(t *testing.T) *redis.Client {
	t.Helper()
	options := &redis.Options{Addr: os.Getenv("AIM_CAPACITY_TEST_REDIS_ADDR"), MaxRetries: -1, ContextTimeoutEnabled: true}
	if options.Addr == "" {
		if binary, err := exec.LookPath("redis-server"); err == nil {
			options.Network = "unix"
			options.Addr = filepath.Join(t.TempDir(), "redis.sock")
			cmd := exec.Command(binary, "--port", "0", "--unixsocket", options.Addr, "--save", "", "--appendonly", "no")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		} else {
			if _, err := exec.LookPath("docker"); err != nil {
				t.Fatal("real Redis required: install redis-server or Docker, or set AIM_CAPACITY_TEST_REDIS_ADDR")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--detach", "--publish", "127.0.0.1::6379", "redis:7-alpine", "redis-server", "--save", "", "--appendonly", "no").CombinedOutput()
			if err != nil {
				t.Fatalf("start real Redis: %v: %s", err, out)
			}
			id := strings.TrimSpace(string(out))
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = exec.CommandContext(cleanup, "docker", "rm", "--force", id).Run()
			})
			out, err = exec.CommandContext(ctx, "docker", "port", id, "6379/tcp").CombinedOutput()
			if err != nil {
				t.Fatalf("discover real Redis port: %v: %s", err, out)
			}
			options.Addr = strings.TrimSpace(string(out))
			if _, _, err := net.SplitHostPort(options.Addr); err != nil {
				t.Fatalf("invalid Redis Docker address %q: %v", options.Addr, err)
			}
		}
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := client.Ping(ctx).Err(); err == nil {
			return client
		}
		select {
		case <-ctx.Done():
			t.Fatalf("real Redis did not become ready: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func capacityConfig(t *testing.T, client *redis.Client) infra.CapacityConfig {
	t.Helper()
	config := infra.CapacityConfig{
		// Quota behavior is not a Redis latency test. Allow script loading and
		// I/O scheduling headroom when other packages or images build in parallel.
		MaxInFlight: 8, LeaseDuration: 5 * time.Second,
		PollInterval: 5 * time.Millisecond,
		KeyPrefix:    "test:{" + uuid.NewString() + "}:capacity",
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, config.KeyPrefix+":*", 100).Result()
			if err != nil {
				t.Errorf("clean isolated capacity namespace: %v", err)
				return
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Errorf("clean isolated capacity keys: %v", err)
					return
				}
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return config
}

func capacityScheduler(t *testing.T, client redis.UniversalClient, config infra.CapacityConfig) *infra.CapacityScheduler {
	t.Helper()
	scheduler, err := infra.NewCapacityScheduler(client, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	return scheduler
}

func acquireCapacity(t *testing.T, scheduler domain.CapacityScheduler, request domain.AdmissionRequest) domain.CapacityPermit {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	permit, err := scheduler.Acquire(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(permit.Release)
	return permit
}

func TestCapacitySharedPoolWaitDoesNotConsumeRequestQuota(t *testing.T) {
	client := capacityRedis(t)
	awaitCapacityRateWindow(t, client, time.Minute, 2*time.Second)
	config := capacityConfig(t, client)
	firstReplica := capacityScheduler(t, client, config)
	secondReplica := capacityScheduler(t, client, config)
	pool := uuid.NewString()
	model := func(id string) *domain.ModelEntry {
		return &domain.ModelEntry{ID: id, ConnectionID: "connection", QuotaPoolID: pool,
			PoolLimits: domain.CapacityLimits{Concurrency: 1, RequestsPerMinute: 2}}
	}
	first := acquireCapacity(t, firstReplica, domain.AdmissionRequest{Model: model("first")})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := secondReplica.Acquire(ctx, domain.AdmissionRequest{Model: model("second")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("occupied shared pool must wait for caller deadline, got %v", err)
	}
	first.Release()
	first.Release()
	second := acquireCapacity(t, secondReplica, domain.AdmissionRequest{Model: model("second")})
	select {
	case <-second.Context().Done():
		t.Fatalf("new permit already canceled: %v", context.Cause(second.Context()))
	default:
	}
	second.Release()
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = firstReplica.Acquire(ctx, domain.AdmissionRequest{Model: model("first")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(fmt.Sprintf("two admitted requests exhaust RPM; got %v", err))
	}
}

func TestCapacityCancellationDoesNotReleaseLiveAttempt(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown=%t", shutdown), func(t *testing.T) {
			client := capacityRedis(t)
			config := capacityConfig(t, client)
			config.MaxInFlight = 1
			first := capacityScheduler(t, client, config)
			second := capacityScheduler(t, client, config)
			request := domain.AdmissionRequest{Model: capacityLeasedModel()}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			permit, err := first.Acquire(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer permit.Release()
			if shutdown {
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case <-permit.Context().Done():
			case <-time.After(3 * time.Second):
				t.Fatal("live attempt did not receive cancellation")
			}
			waiting, stopWaiting := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer stopWaiting()
			unexpected, err := second.Acquire(waiting, request)
			if unexpected != nil {
				unexpected.Release()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancellation is not proof of provider completion; slot must remain held, got %v", err)
			}
			permit.Release()
			acquireCapacity(t, second, request)
		})
	}
}

func capacityModel(id, pool string) *domain.ModelEntry {
	return &domain.ModelEntry{ID: id, ConnectionID: "connection-" + pool, QuotaPoolID: pool}
}

func capacityLeasedModel() *domain.ModelEntry {
	model := capacityModel("model", "pool")
	model.PoolLimits.Concurrency = 1
	return model
}

// Local admission precedes payload validation. An invalid request therefore
// observes Busy only when live attempts and supply waiters fill this process;
// otherwise it fails validation without Redis I/O or a short socket deadline.
func awaitCapacityFull(t *testing.T, scheduler domain.CapacityScheduler) {
	t.Helper()
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for deadline.Err() == nil {
		permit, err := scheduler.Acquire(deadline, domain.AdmissionRequest{})
		if permit != nil {
			permit.Release()
			t.Fatal("invalid admission probe unexpectedly received a permit")
		}
		if errors.Is(err, domain.ErrCapacityBusy) {
			return
		}
		if !errors.Is(err, domain.ErrCapacityInvalidRequest) {
			t.Fatalf("observe local admission bound: %v", err)
		}
		select {
		case <-deadline.Done():
			t.Fatal("intended waiters did not fill local admission")
		case <-ticker.C:
		}
	}
	t.Fatal("intended waiters did not fill local admission")
}

func TestCapacityLocalBoundIncludesSupplierWaitersAndCancellationReleasesIt(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.MaxInFlight = 2
	scheduler := capacityScheduler(t, client, config)
	otherReplica := capacityScheduler(t, client, config)
	request := domain.AdmissionRequest{Model: capacityLeasedModel()}
	held := acquireCapacity(t, scheduler, request)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		permit, err := awaitWaitingCapacity(ctx, scheduler, request)
		if permit != nil {
			permit.Release()
		}
		result <- err
	}()
	awaitCapacityFull(t, scheduler)
	// Even an unrelated unlimited supplier cannot bypass a full local bound.
	unlimited := domain.AdmissionRequest{Model: capacityModel("independent", "independent")}
	if permit, err := scheduler.Acquire(t.Context(), unlimited); permit != nil || !errors.Is(err, domain.ErrCapacityBusy) {
		t.Fatalf("full process must reject immediately: permit=%v err=%v", permit, err)
	}
	// Local admission is independent per replica, never a Redis global bound.
	acquireCapacity(t, otherReplica, unlimited).Release()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("supplier waiter cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supplier cancellation did not return")
	}
	acquireCapacity(t, scheduler, unlimited).Release()
	held.Release()
	acquireCapacity(t, scheduler, request).Release()
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Acquire(t.Context(), unlimited); !errors.Is(err, domain.ErrCapacityClosed) {
		t.Fatalf("closed scheduler must reject new work: %v", err)
	}
}

func TestCapacityUnlimitedSupplyStillHasFiniteLocalAdmission(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.MaxInFlight = 1
	first := capacityScheduler(t, client, config)
	second := capacityScheduler(t, client, config)
	request := domain.AdmissionRequest{Model: capacityModel("unlimited", "pool")}
	held := acquireCapacity(t, first, request)
	if permit, err := first.Acquire(t.Context(), request); permit != nil || !errors.Is(err, domain.ErrCapacityBusy) {
		t.Fatalf("unlimited supply must retain local protection: permit=%v err=%v", permit, err)
	}
	acquireCapacity(t, second, request).Release()
	held.Release()
	held.Release()
	acquireCapacity(t, first, request).Release()
}

func TestCapacityIndependentPoolsDoNotWaitBehindBlockedSupplier(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.MaxInFlight = 3
	scheduler := capacityScheduler(t, client, config)
	request := domain.AdmissionRequest{Model: capacityLeasedModel()}
	held := acquireCapacity(t, scheduler, request)
	unlimited := domain.AdmissionRequest{Model: capacityModel("independent", "idle-pool")}
	independent := acquireCapacity(t, scheduler, unlimited)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		permit, err := awaitWaitingCapacity(ctx, scheduler, request)
		if permit != nil {
			permit.Release()
		}
		finished <- err
	}()
	awaitCapacityFull(t, scheduler)
	independent.Release()
	acquireCapacity(t, scheduler, unlimited).Release()
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked pool waiter: %v", err)
	}
	held.Release()
}

func TestCapacityOptionalModelLimitsAndNoImplicitSupplierCap(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.MaxInFlight = 12
	scheduler := capacityScheduler(t, client, config)
	unlimited := capacityModel("unlimited", "unlimited-pool")
	permits := make([]domain.CapacityPermit, 0, 11)
	for range 11 {
		permits = append(permits, acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: unlimited}))
	}
	for _, permit := range permits {
		permit.Release()
	}
	model := capacityModel("limited", "shared")
	model.PoolLimits.Concurrency = 2
	model.ModelLimits.Concurrency = 1
	first := acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: model})
	other := capacityModel("other", "shared")
	other.PoolLimits = model.PoolLimits
	second := acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: other})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := scheduler.Acquire(ctx, domain.AdmissionRequest{Model: model}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("optional model sublimit must constrain shared-pool borrowing: %v", err)
	}
	second.Release()
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := scheduler.Acquire(ctx, domain.AdmissionRequest{Model: model}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("free pool slot must not bypass model sublimit: %v", err)
	}
	first.Release()
	acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: model})
}

func TestCapacityNewerPoolRevisionCannotBeOverwrittenByStaleReplica(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	first := capacityScheduler(t, client, config)
	stale := capacityScheduler(t, client, config)
	old := capacityModel("old", "shared")
	old.PoolLimits.Concurrency, old.PoolRevision = 2, 1000000000000000001
	held := acquireCapacity(t, first, domain.AdmissionRequest{Model: old})
	newer := *old
	newer.ID, newer.PoolLimits.Concurrency, newer.PoolRevision = "new", 1, 1000000000000000002
	for _, model := range []*domain.ModelEntry{&newer, old} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		_, err := stale.Acquire(ctx, domain.AdmissionRequest{Model: model})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("restrictive revision must remain authoritative, revision %d: %v", model.PoolRevision, err)
		}
	}
	held.Release()
	acquireCapacity(t, stale, domain.AdmissionRequest{Model: old})
}

func TestCapacityLivePermitRenewsWithoutBecomingFree(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.LeaseDuration = time.Second // Exercise renewal across multiple short leases.
	config.MaxInFlight = 1
	first := capacityScheduler(t, client, config)
	second := capacityScheduler(t, client, config)
	request := domain.AdmissionRequest{Model: capacityLeasedModel()}
	held := acquireCapacity(t, first, request)
	ctx, cancel := context.WithTimeout(t.Context(), 2200*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("renewed live permit must not expire into a free slot: %v", err)
	}
	if err := held.Context().Err(); err != nil {
		t.Fatalf("healthy renewal canceled a live attempt: %v", context.Cause(held.Context()))
	}
	held.Release()
	acquireCapacity(t, second, request)
}

type capacityDropReplyConn struct {
	net.Conn
	drop *atomic.Bool
}

func (c *capacityDropReplyConn) Read(buffer []byte) (int, error) {
	n, err := c.Conn.Read(buffer)
	if n > 0 && c.drop.CompareAndSwap(true, false) {
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestCapacityLostRenewalReplyCancelsBeforeGuardedRecovery(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.LeaseDuration = time.Second // Keep injected renewal loss and reclaim grace bounded.
	config.MaxInFlight = 1
	var drop atomic.Bool
	options := *client.Options()
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &capacityDropReplyConn{Conn: conn, drop: &drop}, nil
	}
	faulty := redis.NewClient(&options)
	t.Cleanup(func() { _ = faulty.Close() })
	first := capacityScheduler(t, faulty, config)
	second := capacityScheduler(t, client, config)
	request := domain.AdmissionRequest{Model: capacityLeasedModel()}
	held := acquireCapacity(t, first, request)
	drop.Store(true)
	select {
	case <-held.Context().Done():
		if cause := context.Cause(held.Context()); !errors.Is(cause, domain.ErrCapacityUnavailable) {
			t.Fatalf("lost renewal reply must fail closed: %v", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lost renewal reply did not cancel upstream context")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceling uncertain upstream must not immediately free its slot: %v", err)
	}
	// No explicit release: recover from the lease identity only after its
	// cancellation deadline and full grace interval.
	recovered := acquireCapacity(t, second, request)
	// A predecessor's delayed idempotent release cannot delete a successor.
	held.Release()
	held.Release()
	// Probe from an empty replica so its local bound cannot hide a wrongly
	// released supplier lease behind Busy on the successor's full process.
	probe := capacityScheduler(t, client, config)
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := probe.Acquire(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("old release deleted successor identity: %v", err)
	}
	recovered.Release()
}

func TestCapacityCoordinationErrorsFailClosedAndInvalidRequestsReject(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	options := *client.Options()
	closed := redis.NewClient(&options)
	_ = closed.Close()
	scheduler := capacityScheduler(t, closed, config)
	request := domain.AdmissionRequest{Model: capacityModel("model", "pool")}
	if permit, err := scheduler.Acquire(t.Context(), request); permit != nil || !errors.Is(err, domain.ErrCapacityUnavailable) {
		t.Fatalf("coordination outage must never admit: permit=%v err=%v", permit, err)
	}
	healthy := capacityScheduler(t, client, config)
	for _, invalid := range []domain.AdmissionRequest{
		{},
		{Model: request.Model, InputTokens: -1},
		{Model: &domain.ModelEntry{ID: "missing-connection", QuotaPoolID: "pool"}},
	} {
		if _, err := healthy.Acquire(t.Context(), invalid); !errors.Is(err, domain.ErrCapacityInvalidRequest) {
			t.Fatalf("invalid admission must reject explicitly: %v", err)
		}
	}
	for _, tokens := range []int{0, 4} {
		model := capacityModel("tokens", "token-pool")
		model.PoolLimits.TokensPerSecond = 3
		if _, err := healthy.Acquire(t.Context(), domain.AdmissionRequest{Model: model, InputTokens: tokens}); !errors.Is(err, domain.ErrCapacityInvalidRequest) {
			t.Fatalf("missing/oversized TPS estimate must reject instead of waiting forever: %v", err)
		}
	}
	negative := capacityModel("negative", "negative-pool")
	negative.ModelLimits.Concurrency = -1
	if _, err := healthy.Acquire(t.Context(), domain.AdmissionRequest{Model: negative}); !errors.Is(err, domain.ErrCapacityInvalidRequest) {
		t.Fatalf("negative supplier limits must reject: %v", err)
	}
}

func TestCapacityRejectsNonpositiveLocalAdmissionBound(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	for _, limit := range []int{0, -1} {
		config.MaxInFlight = limit
		if scheduler, err := infra.NewCapacityScheduler(client, config); scheduler != nil || !errors.Is(err, domain.ErrCapacityInvalidRequest) {
			t.Fatalf("nonpositive local bound %d must reject: scheduler=%v err=%v", limit, scheduler, err)
		}
	}
}

// Admission probes may briefly occupy the last local slot before a worker.
// Retry only Busy (nothing dispatched) to establish a supply-wait barrier.
func awaitWaitingCapacity(ctx context.Context, scheduler domain.CapacityScheduler, request domain.AdmissionRequest) (domain.CapacityPermit, error) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		permit, err := scheduler.Acquire(ctx, request)
		if !errors.Is(err, domain.ErrCapacityBusy) {
			return permit, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Synchronize to the Redis clock's actual window condition, never an assumed
// sleep or implementation-specific scheduler key. This avoids crossing a
// minute boundary during a negative quota assertion.
func awaitCapacityRateWindow(t *testing.T, client *redis.Client, period, remaining time.Duration) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		now, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		start := now.Truncate(period)
		if start.Add(period).Sub(now) >= remaining {
			return start
		}
		select {
		case <-ctx.Done():
			t.Fatal("Redis rate window did not become ready")
		case <-ticker.C:
		}
	}
}

func TestCapacitySupplierSecondWindowsAcrossReplicas(t *testing.T) {
	client := capacityRedis(t)
	for _, dimension := range []string{"pool-rps", "model-rps", "pool-tps", "model-tps"} {
		t.Run(dimension, func(t *testing.T) {
			config := capacityConfig(t, client)
			first := capacityScheduler(t, client, config)
			second := capacityScheduler(t, client, config)
			model := capacityModel("model", "pool")
			switch dimension {
			case "pool-rps":
				model.PoolLimits.RequestsPerSecond = 1
			case "model-rps":
				model.ModelLimits.RequestsPerSecond = 1
			case "pool-tps":
				model.PoolLimits.TokensPerSecond = 3
			case "model-tps":
				model.ModelLimits.TokensPerSecond = 3
			}
			start := awaitCapacityRateWindow(t, client, time.Second, 800*time.Millisecond)
			request := domain.AdmissionRequest{Model: model, InputTokens: 2}
			acquireCapacity(t, first, request).Release()
			acquireCapacity(t, second, request).Release()
			now, err := client.Time(t.Context()).Result()
			if err != nil {
				t.Fatal(err)
			}
			if now.Before(start.Add(time.Second)) {
				t.Fatalf("%s overspent the shared Redis second window", dimension)
			}
		})
	}
}

func TestCapacityCrashHelper(t *testing.T) {
	if os.Getenv("AIM_CAPACITY_CRASH_HELPER") != "1" {
		return
	}
	config := infra.CapacityConfig{MaxInFlight: 1,
		LeaseDuration: time.Second, PollInterval: 5 * time.Millisecond,
		KeyPrefix: os.Getenv("AIM_CAPACITY_CRASH_PREFIX")}
	client := redis.NewClient(&redis.Options{Network: os.Getenv("AIM_CAPACITY_CRASH_NETWORK"),
		Addr: os.Getenv("AIM_CAPACITY_CRASH_ADDRESS"), MaxRetries: -1, ContextTimeoutEnabled: true})
	scheduler, err := infra.NewCapacityScheduler(client, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := scheduler.Acquire(ctx, domain.AdmissionRequest{Model: capacityLeasedModel()}); err != nil {
		t.Fatal(err)
	}
	// Deliberately bypass all deferred cleanup and renewal to model a worker
	// disappearing after a real, successful public-interface admission.
	fmt.Println("capacity-admitted-before-crash")
	os.Exit(0)
}

func TestCapacityCrashedReplicaRecoversIdentityLease(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.LeaseDuration = time.Second // Must match the crash helper's short lease.
	config.MaxInFlight = 1
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCapacityCrashHelper$")
	child.Env = append(os.Environ(), "AIM_CAPACITY_CRASH_HELPER=1", "AIM_CAPACITY_CRASH_PREFIX="+config.KeyPrefix,
		"AIM_CAPACITY_CRASH_NETWORK="+client.Options().Network, "AIM_CAPACITY_CRASH_ADDRESS="+client.Options().Addr)
	output, err := child.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "capacity-admitted-before-crash") {
		t.Fatalf("crash helper did not acquire real Redis permit: %v: %s", err, output)
	}
	scheduler := capacityScheduler(t, client, config)
	acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: capacityLeasedModel()})
}

func TestCapacityWaitingRequestUsesNewerObservedPoolLimits(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	config.MaxInFlight = 3
	scheduler := capacityScheduler(t, client, config)
	observer := capacityScheduler(t, client, config)
	old := capacityModel("old", "shared")
	old.PoolLimits.Concurrency, old.PoolRevision = 2, 1
	first := acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: old})
	second := acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: old})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		permit, err := awaitWaitingCapacity(ctx, scheduler, domain.AdmissionRequest{Model: old})
		if permit != nil {
			permit.Release()
		}
		result <- err
	}()
	awaitCapacityFull(t, scheduler)
	newer := *old
	newer.PoolLimits.Concurrency, newer.PoolRevision = 1, 2
	observeCtx, stopObserve := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, observeErr := observer.Acquire(observeCtx, domain.AdmissionRequest{Model: &newer})
	stopObserve()
	if !errors.Is(observeErr, context.DeadlineExceeded) {
		t.Fatalf("observer must publish newer limits while supply remains occupied: %v", observeErr)
	}
	first.Release()
	// One live attempt fills the reduced supplier limit. The existing waiter
	// must not restore its old snapshot and claim the newly released slot.
	select {
	case err := <-result:
		t.Fatalf("stale waiting request bypassed newer limit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel waiting request: %v", err)
	}
	second.Release()
	acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: old})
}

func TestCapacityOptionalModelMinuteLimitIsIsolated(t *testing.T) {
	client := capacityRedis(t)
	awaitCapacityRateWindow(t, client, time.Minute, 2*time.Second)
	config := capacityConfig(t, client)
	scheduler := capacityScheduler(t, client, config)
	limited := capacityModel("limited", "shared")
	limited.ModelLimits.RequestsPerMinute = 1
	request := domain.AdmissionRequest{Model: limited}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scheduler.Acquire(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission must not consume a supplier request: %v", err)
	}
	acquireCapacity(t, scheduler, request).Release()
	acquireCapacity(t, scheduler, domain.AdmissionRequest{Model: capacityModel("other", "shared")}).Release()
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := scheduler.Acquire(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("model RPM must constrain only its model, including after release: %v", err)
	}
}

func TestCapacityLostAdmissionReplyAbandonsIdentityAndRefundsQuota(t *testing.T) {
	client := capacityRedis(t)
	awaitCapacityRateWindow(t, client, time.Minute, 2*time.Second)
	config := capacityConfig(t, client)
	config.MaxInFlight = 1
	healthy := capacityScheduler(t, client, config)
	// Warm the actual Redis script cache via public admission, so the fault
	// drops a committed admission reply rather than a harmless NOSCRIPT reply.
	acquireCapacity(t, healthy, domain.AdmissionRequest{Model: capacityModel("warm", "warm")}).Release()
	var drop atomic.Bool
	options := *client.Options()
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &capacityDropReplyConn{Conn: conn, drop: &drop}, nil
	}
	faulty := redis.NewClient(&options)
	t.Cleanup(func() { _ = faulty.Close() })
	if err := faulty.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	scheduler := capacityScheduler(t, faulty, config)
	model := capacityModel("model", "pool")
	model.PoolLimits.RequestsPerMinute = 1
	request := domain.AdmissionRequest{Model: model}
	drop.Store(true)
	if permit, err := scheduler.Acquire(t.Context(), request); permit != nil || !errors.Is(err, domain.ErrCapacityUnavailable) {
		t.Fatalf("uncertain admission must not dispatch a provider attempt: permit=%v err=%v", permit, err)
	}
	acquireCapacity(t, healthy, request).Release()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := healthy.Acquire(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abandoned identity must refund exactly once, not over-credit quota: %v", err)
	}
}

func TestCapacityAbortRefundsUndispatchedReservationButReleaseRetainsUsage(t *testing.T) {
	client := capacityRedis(t)
	for _, dimension := range []string{"pool-rpm", "model-rpm", "pool-rps", "model-rps", "pool-tps", "model-tps"} {
		t.Run(dimension, func(t *testing.T) {
			config := capacityConfig(t, client)
			config.MaxInFlight = 1
			first := capacityScheduler(t, client, config)
			second := capacityScheduler(t, client, config)
			model := capacityLeasedModel()
			model.ModelLimits.Concurrency = 1
			limits := &model.PoolLimits
			if strings.HasPrefix(dimension, "model-") {
				limits = &model.ModelLimits
			}
			period := time.Second
			switch {
			case strings.HasSuffix(dimension, "rpm"):
				limits.RequestsPerMinute = 1
				period = time.Minute
			case strings.HasSuffix(dimension, "rps"):
				limits.RequestsPerSecond = 1
			case strings.HasSuffix(dimension, "tps"):
				limits.TokensPerSecond = 2
			}
			awaitCapacityRateWindow(t, client, period, 800*time.Millisecond)
			request := domain.AdmissionRequest{Model: model, InputTokens: 2}
			undispatched := acquireCapacity(t, first, request)
			undispatched.Abort()
			undispatched.Abort()
			undispatched.Release()
			if undispatched.Context().Err() == nil {
				t.Fatal("Abort must cancel the undispatched permit")
			}
			// Same-process reacquisition proves Abort released the local slot,
			// both supplier concurrency dimensions, and the rate reservation.
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			dispatched, err := first.Acquire(ctx, request)
			if err != nil {
				cancel()
				t.Fatalf("undispatched %s reservation was not refunded: %v", dimension, err)
			}
			dispatched.Release()
			dispatched.Abort() // A later Abort must not refund dispatched usage.
			cancel()
			ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if permit, err := second.Acquire(ctx, request); permit != nil || !errors.Is(err, context.DeadlineExceeded) {
				if permit != nil {
					permit.Release()
				}
				t.Fatalf("normal Release must retain %s usage exactly once: permit=%v err=%v", dimension, permit, err)
			}
		})
	}
}

// Model a caller whose deadline passed before its timer callback published Err.
// This is the same boundary that can make a Redis socket timeout win the race.
type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestCapacityElapsedCallerDeadlineIsNotCoordinationFailure(t *testing.T) {
	client := capacityRedis(t)
	config := capacityConfig(t, client)
	scheduler := capacityScheduler(t, client, config)
	ctx := pendingDeadlineContext{Context: t.Context(), deadline: time.Now().Add(-time.Second)}
	if permit, err := scheduler.Acquire(ctx, domain.AdmissionRequest{Model: capacityModel("model", "pool")}); permit != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("elapsed caller deadline must retain its context classification: permit=%v err=%v", permit, err)
	}
}
