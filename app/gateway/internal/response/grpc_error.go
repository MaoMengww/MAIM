package response

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/pkg/errors"
)

// GRPCError handles errors returned from gRPC calls and maps them to HTTP responses.
// It extracts the business error code and message from the gRPC status error
// and writes an appropriate JSON error response.
func GRPCError(c *gin.Context, err error) {
	code, msg := errors.FromGRPCStatus(err)
	httpStatus := businessCodeToHTTP(code)
	Error(c, httpStatus, code, msg)
}

func businessCodeToHTTP(code int) int {
	switch code {
	case errors.CodeInvalidParam:
		return StatusBadRequest
	case errors.CodeUnauthorized:
		return StatusUnauthorized
	case errors.CodeForbidden:
		return StatusForbidden
	case errors.CodeNotFound:
		return StatusNotFound
	case errors.CodeConflict:
		return StatusConflict
	case errors.CodeTooManyRequests:
		return 429
	case errors.CodeServiceDown:
		return 503
	case errors.CodeBotNotFound:
		return StatusNotFound
	case errors.CodeBotForbidden:
		return StatusForbidden
	case errors.CodeWebhookInvalid:
		return StatusBadRequest
	case errors.CodeWebhookTimeout:
		return StatusRequestTimeout
	default:
		return StatusInternal
	}
}
