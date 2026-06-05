package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetFileInfoLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetFileInfoLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetFileInfoLogic {
	return &GetFileInfoLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetFileInfoLogic) GetFileInfo(in *filepb.GetFileInfoReq) (*filepb.GetFileInfoResp, error) {
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		return nil, ErrFileNotFound
	}

	// Check access
	if f.Access != int32(filepb.FileAccess_FILE_ACCESS_PUBLIC) && f.UploaderID != in.GetUserId() {
		return nil, ErrAccessDenied
	}

	return &filepb.GetFileInfoResp{File: toProtoFileInfo(f)}, nil
}
