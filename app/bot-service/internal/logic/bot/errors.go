package bot

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrBotInvalid     = errors.New(errors.CodeInvalidParam, "invalid bot request")
	ErrBotNotFound    = errors.New(errors.CodeBotNotFound, "bot not found")
	ErrBotForbidden   = errors.New(errors.CodeBotForbidden, "operation not allowed for this bot")
	ErrWebhookInvalid = errors.New(errors.CodeWebhookInvalid, "webhook signature invalid")
	ErrWebhookTimeout = errors.New(errors.CodeWebhookTimeout, "webhook timestamp expired")
)
