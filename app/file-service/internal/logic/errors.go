package logic

import pkg_errors "github.com/maomeng/aim/pkg/errors"

var (
	ErrInvalidParam    = pkg_errors.ErrInvalidParam
	ErrFileNotFound    = pkg_errors.New(1004, "file not found")
	ErrAccessDenied    = pkg_errors.New(1003, "access denied")
	ErrNotUploader     = pkg_errors.New(1003, "not the uploader")
	ErrUploadFailed    = pkg_errors.New(1014, "upload failed")
	ErrMD5Mismatch     = pkg_errors.New(1001, "md5 mismatch")
	ErrUnsupportedType = pkg_errors.New(1001, "unsupported mime type")
)
