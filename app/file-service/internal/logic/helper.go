package logic

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"
	"time"

	"github.com/maomeng/aim/app/file-service/internal/model"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func formatObjectKey(fileID int64, ext string) string {
	date := time.Now().Format("2006/01/02")
	return fmt.Sprintf("files/%s/%d%s", date, fileID, ext)
}

func extFromName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return ""
	}
	return ext
}

func grpcError(err error) error {
	if err == nil {
		return nil
	}
	bizErr, ok := pkg_errors.IsBizError(err)
	if !ok {
		return status.Error(codes.Internal, err.Error())
	}
	switch bizErr.Code {
	case 1001: // invalid param / unsupported type / md5 mismatch
		return status.Error(codes.InvalidArgument, bizErr.Message)
	case 1003: // access denied / not uploader
		return status.Error(codes.PermissionDenied, bizErr.Message)
	case 1004: // not found
		return status.Error(codes.NotFound, bizErr.Message)
	case 1014: // upload failed
		return status.Error(codes.Internal, bizErr.Message)
	default:
		return status.Error(codes.Internal, bizErr.Message)
	}
}

func decodeImageDimensions(data []byte) (int32, int32) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return int32(cfg.Width), int32(cfg.Height)
}

func toProtoFileInfo(f *model.File) *filepb.FileInfo {
	return &filepb.FileInfo{
		FileId:     f.ID,
		Name:       f.Name,
		Key:        f.Key,
		Size:       f.Size,
		MimeType:   f.MimeType,
		Ext:        f.Ext,
		Width:      f.Width,
		Height:     f.Height,
		Duration:   f.Duration,
		Md5:        f.Md5,
		Purpose:    filepb.FilePurpose(f.Purpose),
		Access:     filepb.FileAccess(f.Access),
		UploaderId: f.UploaderID,
		Bucket:     f.Bucket,
		CreatedAt:  f.CreatedAt.Unix(),
	}
}
