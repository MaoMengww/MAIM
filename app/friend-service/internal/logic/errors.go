package logic

import pkg_errors "github.com/maomeng/aim/pkg/errors"

var (
	ErrUnauthenticated       = pkg_errors.New(pkg_errors.CodeUnauthorized, "unauthenticated")
	ErrInvalidParam          = pkg_errors.New(pkg_errors.CodeInvalidParam, "invalid parameter")
	ErrCannotFriendSelf      = pkg_errors.New(pkg_errors.CodeInvalidParam, "cannot friend yourself")
	ErrAlreadyFriend         = pkg_errors.New(pkg_errors.CodeConflict, "already friend")
	ErrNotFriend             = pkg_errors.New(pkg_errors.CodeNotFound, "not friend")
	ErrBlocked               = pkg_errors.New(pkg_errors.CodeConflict, "blocked")
	ErrRequestAlreadySent    = pkg_errors.New(pkg_errors.CodeConflict, "request already sent")
	ErrRequestNotFound       = pkg_errors.New(pkg_errors.CodeNotFound, "request not found")
	ErrNotRecipient          = pkg_errors.New(pkg_errors.CodeNotFound, "not the recipient")
	ErrNotSender             = pkg_errors.New(pkg_errors.CodeNotFound, "not the sender")
	ErrRequestAlreadyHandled = pkg_errors.New(pkg_errors.CodeConflict, "request already handled")
	ErrGroupNotFound         = pkg_errors.New(pkg_errors.CodeNotFound, "group not found")
	ErrNotGroupOwner         = pkg_errors.New(pkg_errors.CodeForbidden, "not group owner")
)
