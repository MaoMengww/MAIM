package domain

import "github.com/maomeng/aim/pkg/errors"

var (
	ErrKBNotFound         = errors.New(errors.CodeNotFound, "knowledge base not found")
	ErrDocNotFound        = errors.New(errors.CodeNotFound, "document not found")
	ErrDocumentProcessing = errors.New(errors.CodeConflict, "document is being processed, cannot delete")
	ErrForbidden          = errors.New(errors.CodeForbidden, "permission denied")
	ErrInvalidFileType    = errors.New(errors.CodeInvalidParam, "invalid file type")
	ErrFileTooLarge       = errors.New(errors.CodeInvalidParam, "file too large")
	ErrDocumentNotFailed  = errors.New(errors.CodeInvalidParam, "document is not in failed status")
	ErrDuplicateBinding   = errors.New(errors.CodeConflict, "binding already exists")
	ErrBindFailed         = errors.New(errors.CodeInternal, "bind failed")
	ErrUnbindFailed       = errors.New(errors.CodeInternal, "unbind failed")
	ErrParserUnsupported  = errors.New(errors.CodeInvalidParam, "parser cannot handle this document")
)
