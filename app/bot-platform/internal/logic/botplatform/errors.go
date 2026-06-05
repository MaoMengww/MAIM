package botplatform

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrBotNotFound    = errors.New(errors.CodeBotNotFound, "bot not found")
	ErrBotForbidden   = errors.New(errors.CodeBotForbidden, "operation not allowed for this bot")
	ErrWebhookInvalid = errors.New(errors.CodeWebhookInvalid, "webhook signature invalid")
	ErrWebhookTimeout = errors.New(errors.CodeWebhookTimeout, "webhook timestamp expired")
)
