package logic

import pkg_errors "github.com/maomeng/aim/pkg/errors"

var (
	ErrInvalidParam    = pkg_errors.ErrInvalidParam
	ErrFileNotFound    = pkg_errors.New(pkg_errors.CodeNotFound, "file not found")
	ErrAccessDenied    = pkg_errors.New(pkg_errors.CodeForbidden, "access denied")
	ErrNotUploader     = pkg_errors.New(pkg_errors.CodeForbidden, "not the uploader")
	ErrUploadFailed    = pkg_errors.New(pkg_errors.CodeIOError, "upload failed")
	ErrMD5Mismatch     = pkg_errors.New(pkg_errors.CodeInvalidParam, "md5 mismatch")
	ErrUnsupportedType = pkg_errors.New(pkg_errors.CodeInvalidParam, "unsupported mime type")
)
