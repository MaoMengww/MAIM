package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetDownloadURLLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetDownloadURLLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetDownloadURLLogic {
	return &GetDownloadURLLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetDownloadURLLogic) GetDownloadURL(in *filepb.GetDownloadURLReq) (*filepb.GetDownloadURLResp, error) {
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		l.Errorf("file not found for download URL: file_id=%d err=%v", in.GetFileId(), err)
		return nil, ErrFileNotFound
	}

	// Check access: public files can be downloaded by anyone
	if f.Access != int32(filepb.FileAccess_FILE_ACCESS_PUBLIC) && f.UploaderID != in.GetUserId() {
		return nil, ErrAccessDenied
	}

	l.Infof("download URL generated: file_id=%d", in.GetFileId())
	downloadURL := l.svcCtx.MinIO.PublicURL(f.Key)

	return &filepb.GetDownloadURLResp{
		DownloadUrl: downloadURL,
		ExpiresAt:   0, // permanent
		File:        toProtoFileInfo(f),
	}, nil
}
