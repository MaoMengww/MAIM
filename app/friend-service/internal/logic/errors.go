package logic

import pkg_errors "github.com/maomeng/aim/pkg/errors"

var (
	ErrUnauthenticated       = pkg_errors.New(1002, "unauthenticated")
	ErrInvalidParam          = pkg_errors.New(1001, "invalid parameter")
	ErrCannotFriendSelf      = pkg_errors.New(1001, "cannot friend yourself")
	ErrAlreadyFriend         = pkg_errors.New(1005, "already friend")
	ErrNotFriend             = pkg_errors.New(1004, "not friend")
	ErrBlocked               = pkg_errors.New(1005, "blocked")
	ErrRequestAlreadySent    = pkg_errors.New(1005, "request already sent")
	ErrRequestNotFound       = pkg_errors.New(1004, "request not found")
	ErrNotRecipient          = pkg_errors.New(1004, "not the recipient")
	ErrNotSender             = pkg_errors.New(1004, "not the sender")
	ErrRequestAlreadyHandled = pkg_errors.New(1005, "request already handled")
	ErrGroupNotFound         = pkg_errors.New(1004, "group not found")
	ErrNotGroupOwner         = pkg_errors.New(1003, "not group owner")
)
