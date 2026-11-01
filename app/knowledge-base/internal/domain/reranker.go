package domain

import (
	"context"

	"github.com/maomeng/aim/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RerankRequest struct {
	Method     string
	ModelID    string
	OwnerID    *string
	Query      string
	Candidates []RerankCandidate
	TopK       int
	Authorize  func(context.Context) error
}

type RerankCandidate struct {
	Index   int
	Content string
}

type RerankResult struct {
	Index   int
	Score   float32
	Content string
}

type Reranker interface {
	Rerank(ctx context.Context, req *RerankRequest) ([]RerankResult, error)
}

// RerankMustAbort distinguishes request/eligibility failures from inference
// failures. Only the latter may retain the original candidate order.
func RerankMustAbort(err error) bool {
	switch status.Code(errors.ToGRPCError(err)) {
	case codes.PermissionDenied, codes.Unauthenticated, codes.InvalidArgument,
		codes.NotFound, codes.FailedPrecondition, codes.Canceled, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
