package logic

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrBotNotFound               = errors.New(errors.CodeBotNotFound, "bot not found")
	ErrBotDisabled               = errors.New(errors.CodeForbidden, "bot is disabled")
	ErrConvBotNotFound           = errors.New(errors.CodeNotFound, "conversation bot not found")
	ErrLLMTimeout                = errors.New(errors.CodeTimeout, "LLM request timeout")
	ErrLLMCallFailed             = errors.New(errors.CodeRPCError, "LLM call failed")
	ErrMemoryExtractFailed       = errors.New(errors.CodeInternal, "memory extraction failed")
	ErrMemoryNotFound            = errors.New(errors.CodeNotFound, "memory not found")
	ErrMemoryNotOwner            = errors.New(errors.CodeForbidden, "not the owner of this memory")
	ErrMemoryStoreFailed         = errors.New(errors.CodeDBError, "memory store operation failed")
	ErrGraphExecuteFailed        = errors.New(errors.CodeInternal, "graph execution failed")
	ErrToolNotAvailable          = errors.New(errors.CodeServiceDown, "tool not available")
	ErrKnowledgeUnavailable      = errors.New(errors.CodeServiceDown, "knowledge base unavailable")
	ErrMessageServiceUnavailable = errors.New(errors.CodeServiceDown, "message service unavailable")
	ErrIdempotentCheckFailed     = errors.New(errors.CodeInternal, "idempotent check failed")
)
