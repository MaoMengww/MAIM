package logic

import (
	"fmt"
	"context"
	"time"

	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetUploadURLLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetUploadURLLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetUploadURLLogic {
	return &GetUploadURLLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetUploadURLLogic) GetUploadURL(in *filepb.GetUploadURLReq) (*filepb.GetUploadURLResp, error) {
	if in.GetName() == "" || in.GetSize() <= 0 {
		return nil, ErrInvalidParam
	}

	fileID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate file id failed: %w", err)
	}
	ext := extFromName(in.GetName())
	key := formatObjectKey(fileID, ext)

	expiry := time.Duration(in.GetExpiresIn()) * time.Second
	if expiry <= 0 {
		expiry = time.Hour
	}

	uploadURL, err := l.svcCtx.MinIO.PresignedPutURL(l.ctx, key, expiry)
	if err != nil {
		l.Errorf("failed to generate upload URL: file_name=%s err=%v", in.GetName(), err)
		return nil, err
	}

	now := time.Now()
	f := &model.File{
		ID:         fileID,
		Name:       in.GetName(),
		Key:        key,
		Size:       in.GetSize(),
		MimeType:   in.GetMimeType(),
		Ext:        ext,
		Purpose:    int32(in.GetPurpose()),
		Access:     int32(in.GetAccess()),
		UploaderID: in.GetUploaderId(),
		Bucket:     l.svcCtx.MinIO.Bucket(),
		CreatedAt:  now,
	}
	if err := l.svcCtx.FileRepo.Create(l.ctx, f); err != nil {
		l.Errorf("failed to save file record: file_id=%d file_name=%s err=%v", fileID, in.GetName(), err)
		return nil, err
	}

	l.Infof("upload URL generated: file_id=%d file_name=%s", fileID, in.GetName())
	expiresAt := now.Add(expiry)
	return &filepb.GetUploadURLResp{
		FileId:    fileID,
		UploadUrl: uploadURL,
		Key:       key,
		ExpiresAt: expiresAt.Unix(),
	}, nil
}
