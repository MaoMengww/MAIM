package conversationservice

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrConvNotFound       = errors.New(errors.CodeNotFound, "conversation not found")
	ErrConvCreateFailed   = errors.New(errors.CodeInternal, "create conversation failed")
	ErrConvUpdateFailed   = errors.New(errors.CodeInternal, "update conversation failed")
	ErrConvDeleteFailed   = errors.New(errors.CodeInternal, "delete conversation failed")
	ErrNotMember          = errors.New(errors.CodeForbidden, "not a member of the conversation")
	ErrInsufficientPerm   = errors.New(errors.CodeForbidden, "insufficient permissions")
	ErrMemberNotFound     = errors.New(errors.CodeNotFound, "member not found")
	ErrMemberAddFailed    = errors.New(errors.CodeInternal, "add member failed")
	ErrMemberRemoveFailed = errors.New(errors.CodeInternal, "remove member failed")
	ErrConvMaxMembers     = errors.New(errors.CodeForbidden, "conversation has reached maximum members")
	ErrInvalidConvType    = errors.New(errors.CodeInvalidParam, "invalid conversation type")
	ErrInvalidParam       = errors.New(errors.CodeInvalidParam, "invalid parameter")
	ErrConvMuteFailed     = errors.New(errors.CodeInternal, "mute operation failed")
	ErrConvTransferFailed = errors.New(errors.CodeInternal, "transfer owner failed")
)
