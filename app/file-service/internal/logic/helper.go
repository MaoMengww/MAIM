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
	return pkg_errors.ToGRPCError(err)
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
