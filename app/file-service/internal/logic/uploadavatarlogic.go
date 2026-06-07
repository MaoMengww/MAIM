package logic

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/app/file-service/internal/metrics"
	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type UploadAvatarLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUploadAvatarLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UploadAvatarLogic {
	return &UploadAvatarLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *UploadAvatarLogic) UploadAvatar(in *filepb.UploadAvatarReq) (*filepb.UploadAvatarResp, error) {
	data := in.GetData()
	if len(data) == 0 {
		return nil, grpcError(ErrInvalidParam)
	}

	mimeType := in.GetMimeType()
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, grpcError(ErrUnsupportedType)
	}

	fileID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate file id failed: %w", err)
	}
	ext := mimeToExt(mimeType)
	key := formatObjectKey(fileID, ext)

	info, err := l.svcCtx.MinIO.Upload(l.ctx, key, bytes.NewReader(data), int64(len(data)), mimeType)
	if err != nil {
		return nil, grpcError(ErrUploadFailed)
	}

	width, height := decodeImageDimensions(data)

	thumbnailURL := l.generateThumbnail(data, fileID, ext, in)

	now := time.Now()
	f := &model.File{
		ID:         fileID,
		Name:       fmt.Sprintf("avatar_%d%s", fileID, ext),
		Key:        key,
		Size:       info.Size,
		MimeType:   mimeType,
		Ext:        ext,
		Width:      width,
		Height:     height,
		Purpose:    int32(filepb.FilePurpose_FILE_PURPOSE_AVATAR),
		Access:     int32(filepb.FileAccess_FILE_ACCESS_PUBLIC),
		UploaderID: in.GetUserId(),
		Bucket:     l.svcCtx.MinIO.Bucket(),
		CreatedAt:  now,
	}
	if err := l.svcCtx.FileRepo.Create(l.ctx, f); err != nil {
		return nil, grpcError(err)
	}

	downloadURL := l.svcCtx.MinIO.PublicURL(key)

	metrics.FileUploadTotal.Inc("avatar")
	metrics.FileStorageBytes.Add(float64(info.Size), "avatar")

	l.Infof("avatar uploaded: user_id=%d file_id=%d", in.GetUserId(), fileID)
	return &filepb.UploadAvatarResp{
		FileId:       fileID,
		Url:          downloadURL,
		ThumbnailUrl: thumbnailURL,
		Width:        f.Width,
		Height:       f.Height,
	}, nil
}

func mimeToExt(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func (l *UploadAvatarLogic) generateThumbnail(data []byte, fileID int64, _ string, in *filepb.UploadAvatarReq) string {
	thumbnailKey := fmt.Sprintf("thumbnails/%s/%d_thumb.jpg", time.Now().Format("2006/01/02"), fileID)

	targetSize := int32(200)
	if in.TargetSize != nil && in.GetTargetSize() > 0 && in.GetTargetSize() <= 512 {
		targetSize = in.GetTargetSize()
	}

	thumbData, err := generateThumbnail(data, targetSize, targetSize)
	if err != nil {
		logx.WithContext(l.ctx).Infof("thumbnail generation failed for file %d: %v", fileID, err)
		return ""
	}

	if _, err := l.svcCtx.MinIO.Upload(l.ctx, thumbnailKey, bytes.NewReader(thumbData), int64(len(thumbData)), "image/jpeg"); err != nil {
		logx.WithContext(l.ctx).Infof("thumbnail upload failed for file %d: %v", fileID, err)
		return ""
	}

	return l.svcCtx.MinIO.PublicURL(thumbnailKey)
}
