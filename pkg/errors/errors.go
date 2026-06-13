package errors

import (
	stderrors "errors"
	"fmt"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	CodeUnknown         = 1000
	CodeInvalidParam    = 1001
	CodeUnauthorized    = 1002
	CodeForbidden       = 1003
	CodeNotFound        = 1004
	CodeConflict        = 1005
	CodeInternal        = 1006
	CodeTimeout         = 1007
	CodeTooManyRequests = 1008
	CodeServiceDown     = 1009
	CodeDBError         = 1010
	CodeCacheError      = 1011
	CodeMQError         = 1012
	CodeRPCError        = 1013
	CodeIOError         = 1014
	CodeBotNotFound     = 1015
	CodeBotForbidden    = 1016
	CodeWebhookInvalid  = 1017
	CodeWebhookTimeout  = 1018
)

type BizError struct {
	Code    int
	Message string
	Cause   error
}

func (e *BizError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("code: %d, msg: %s, cause: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("code: %d, msg: %s", e.Code, e.Message)
}

func (e *BizError) Unwrap() error {
	return e.Cause
}

// GRPCCode maps the business error code to a gRPC status code.
func (e *BizError) GRPCCode() codes.Code {
	return GRPCCodeFromCode(e.Code)
}

// GRPCCodeFromCode maps a business error code to a gRPC status code.
func GRPCCodeFromCode(code int) codes.Code {
	switch code {
	case CodeInvalidParam:
		return codes.InvalidArgument
	case CodeUnauthorized:
		return codes.Unauthenticated
	case CodeForbidden:
		return codes.PermissionDenied
	case CodeNotFound:
		return codes.NotFound
	case CodeConflict:
		return codes.AlreadyExists
	case CodeTimeout:
		return codes.DeadlineExceeded
	case CodeTooManyRequests:
		return codes.ResourceExhausted
	case CodeServiceDown, CodeRPCError:
		return codes.Unavailable
	case CodeBotNotFound:
		return codes.NotFound
	case CodeBotForbidden:
		return codes.PermissionDenied
	case CodeWebhookInvalid:
		return codes.InvalidArgument
	case CodeWebhookTimeout:
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}

// HTTPStatusFromCode maps a business error code to an HTTP status code.
func HTTPStatusFromCode(code int) int {
	switch code {
	case CodeInvalidParam, CodeWebhookInvalid:
		return 400
	case CodeUnauthorized:
		return 401
	case CodeForbidden, CodeBotForbidden:
		return 403
	case CodeNotFound, CodeBotNotFound:
		return 404
	case CodeConflict:
		return 409
	case CodeTimeout, CodeWebhookTimeout:
		return 408
	case CodeTooManyRequests:
		return 429
	case CodeServiceDown, CodeRPCError:
		return 503
	default:
		return 500
	}
}

func New(code int, message string) *BizError {
	return &BizError{Code: code, Message: message}
}

func Wrap(code int, message string, cause error) *BizError {
	return &BizError{Code: code, Message: message, Cause: cause}
}

func IsBizError(err error) (*BizError, bool) {
	if err == nil {
		return nil, false
	}
	var bizErr *BizError
	if stderrors.As(err, &bizErr) {
		return bizErr, true
	}
	return nil, false
}

// ToGRPCError converts an error to a gRPC status error.
// If the error is a BizError, it maps the code to a gRPC status code.
// Otherwise, it returns a gRPC Internal error without leaking internals.
func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if be, ok := IsBizError(err); ok {
		st := status.New(be.GRPCCode(), be.Message)
		stWithDetails, detailErr := st.WithDetails(&errdetails.ErrorInfo{
			Reason: "BIZ_ERROR",
			Metadata: map[string]string{
				"biz_code": strconv.Itoa(be.Code),
			},
		})
		if detailErr == nil {
			return stWithDetails.Err()
		}
		return st.Err()
	}
	return status.Error(codes.Internal, "internal server error")
}

// FromGRPCStatus extracts the business error code and message from a gRPC status error.
// Used by the Gateway layer to translate gRPC errors to HTTP responses.
func FromGRPCStatus(err error) (code int, message string) {
	if err == nil {
		return 0, "ok"
	}
	st, ok := status.FromError(err)
	if !ok {
		return CodeInternal, "internal server error"
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Reason != "BIZ_ERROR" {
			continue
		}
		if value := info.Metadata["biz_code"]; value != "" {
			if bizCode, parseErr := strconv.Atoi(value); parseErr == nil {
				return bizCode, st.Message()
			}
		}
	}
	return CodeFromGRPCCode(st.Code()), st.Message()
}

// CodeFromGRPCCode maps a gRPC status code back to a business error code.
func CodeFromGRPCCode(c codes.Code) int {
	switch c {
	case codes.InvalidArgument:
		return CodeInvalidParam
	case codes.Unauthenticated:
		return CodeUnauthorized
	case codes.PermissionDenied:
		return CodeForbidden
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeConflict
	case codes.DeadlineExceeded:
		return CodeTimeout
	case codes.Unavailable:
		return CodeServiceDown
	default:
		return CodeInternal
	}
}

var (
	ErrUnknown         = New(CodeUnknown, "unknown error")
	ErrInvalidParam    = New(CodeInvalidParam, "invalid parameter")
	ErrUnauthorized    = New(CodeUnauthorized, "unauthorized")
	ErrForbidden       = New(CodeForbidden, "forbidden")
	ErrNotFound        = New(CodeNotFound, "not found")
	ErrConflict        = New(CodeConflict, "resource conflict")
	ErrInternal        = New(CodeInternal, "internal server error")
	ErrTimeout         = New(CodeTimeout, "request timeout")
	ErrTooManyRequests = New(CodeTooManyRequests, "too many requests")
	ErrServiceDown     = New(CodeServiceDown, "service unavailable")
	ErrDBError         = New(CodeDBError, "database error")
	ErrCacheError      = New(CodeCacheError, "cache error")
	ErrMQError         = New(CodeMQError, "message queue error")
	ErrRPCError        = New(CodeRPCError, "rpc call error")
	ErrIOError         = New(CodeIOError, "io error")
	ErrBotNotFound     = New(CodeBotNotFound, "bot not found")
	ErrBotForbidden    = New(CodeBotForbidden, "operation not allowed for this bot")
	ErrWebhookInvalid  = New(CodeWebhookInvalid, "webhook signature invalid")
	ErrWebhookTimeout  = New(CodeWebhookTimeout, "webhook timestamp expired")
)
