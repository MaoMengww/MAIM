package handler

import (
	"context"
	stderrors "errors"

	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	"google.golang.org/grpc/status"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// acquireCapacity reserves one actual provider attempt, never a queued business task.
// Serialized UTF-8 bytes deliberately overestimate ordinary text token counts;
// supplier TPS remains an explicitly configured admission budget, not reported usage.
func (h *LLMGatewayHandler) acquireCapacity(ctx context.Context, info *modelEntryInfo, request proto.Message) (domain.CapacityPermit, error) {
	if h.svcCtx.Capacity == nil {
		return nil, errors.New(errors.CodeInternal, "model capacity unavailable")
	}
	inputTokens := 0
	if info.PoolLimits.TokensPerSecond > 0 || info.ModelLimits.TokensPerSecond > 0 {
		encoded, err := protojson.Marshal(request)
		if err != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid model request")
		}
		inputTokens = 2*len(encoded) + 1024
	}
	permit, err := h.svcCtx.Capacity.Acquire(ctx, domain.AdmissionRequest{
		Model: info.ModelEntry, InputTokens: inputTokens,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		if stderrors.Is(err, domain.ErrCapacityBusy) {
			return nil, errors.New(errors.CodeServiceDown, "model capacity busy")
		}
		if stderrors.Is(err, domain.ErrCapacityInvalidRequest) {
			return nil, errors.New(errors.CodeInvalidParam, "request exceeds configured model capacity")
		}
		return nil, errors.New(errors.CodeInternal, "model capacity coordination unavailable")
	}
	// Eligibility and credential routing may change while awaiting supplier capacity.
	current, err := h.svcCtx.ModelRepo.FindByID(permit.Context(), info.ID)
	if err != nil {
		permit.Abort()
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, errors.New(errors.CodeInternal, "model eligibility unavailable")
	}
	if current == nil || current.Status != "active" || current.OwnerType != info.OwnerType || !sameOptionalOwner(current.OwnerID, info.OwnerID) || current.Capability != info.Capability {
		permit.Abort()
		return nil, errors.ErrForbidden
	}
	currentURL := current.BaseURL
	if info.Capability == "chat" {
		currentURL, err = llm.ChatBaseURL(current.Provider, current.BaseURL)
	}
	if err != nil || current.ConnectionID != info.ConnectionID || current.QuotaPoolID != info.QuotaPoolID || current.Provider != info.Provider || current.APIKey != info.APIKey || currentURL != info.BaseURL || current.PoolLimits != info.PoolLimits || current.ModelLimits != info.ModelLimits {
		permit.Abort()
		return nil, errors.New(errors.CodeConflict, "model connection or capacity changed while waiting")
	}
	if err := permit.Context().Err(); err != nil {
		permit.Abort()
		return nil, err
	}
	return permit, nil
}

// RequestError is produced by local validation before provider dispatch. All
// uncertain transport/provider outcomes retain consumption conservatively.
func finishCapacityAttempt(permit domain.CapacityPermit, err error) {
	var invalid *llm.RequestError
	if stderrors.As(err, &invalid) {
		permit.Abort()
		return
	}
	permit.Release()
}

func sameOptionalOwner(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
