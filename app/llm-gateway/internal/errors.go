package llmgateway

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrModelNotFound    = errors.New(errors.CodeNotFound, "model not found")
	ErrProviderNotFound = errors.New(errors.CodeNotFound, "provider not found")
	ErrRateLimited      = errors.New(errors.CodeTooManyRequests, "rate limited")
	ErrEncryptionFailed = errors.New(errors.CodeInternal, "encryption failed")
	ErrBillingFailed    = errors.New(errors.CodeInternal, "billing record failed")
	ErrModelDisabled    = errors.New(errors.CodeForbidden, "model is disabled")
)
