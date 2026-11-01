package domain

import (
	"context"
	"errors"
)

// CapacityLimits describe supplier capacity, not deployment safety limits.
// Zero disables a dimension; negative values are invalid. TPS charges the
// conservative pre-dispatch input estimate, not eventual billed output tokens.
type CapacityLimits struct {
	RequestsPerMinute int
	RequestsPerSecond int
	TokensPerSecond   int
	Concurrency       int
}

var (
	ErrCapacityBusy           = errors.New("local model capacity is full")
	ErrCapacityUnavailable    = errors.New("capacity coordination unavailable")
	ErrCapacityClosed         = errors.New("capacity scheduler closed")
	ErrCapacityInvalidRequest = errors.New("invalid capacity admission request")
	ErrCapacityLeaseLost      = errors.New("capacity permit lease lost")
)

// AdmissionRequest identifies an already authorized, resolved model.
// InputTokens must be positive when TPS is configured.
type AdmissionRequest struct {
	Model       *ModelEntry
	InputTokens int
}

// CapacityPermit bounds one provider attempt, including its streaming lifetime.
// Callers must use Context for upstream I/O. Abort is only for work known not
// to have been dispatched: it refunds supplier request/token reservations.
// Release follows a dispatched attempt/stream closing and retains rate usage.
// Abort and Release are mutually idempotent; the first call wins and cancels
// Context. Cancellation alone cannot prove the provider stopped executing.
type CapacityPermit interface {
	Context() context.Context
	Release()
	Abort()
}

// CapacityScheduler bounds waiting plus executing requests per process and
// coordinates supplier limits across replicas. Local admission never waits;
// supplier capacity waits only within the caller's context. Coordination
// failures fail closed. Close cancels this replica's waiters and permits;
// it does not close the supplied Redis client.
type CapacityScheduler interface {
	Acquire(context.Context, AdmissionRequest) (CapacityPermit, error)
	Close() error
}
