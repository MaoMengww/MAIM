package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteFileLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteFileLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteFileLogic {
	return &DeleteFileLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteFileLogic) DeleteFile(in *filepb.DeleteFileReq) (*common.BaseResponse, error) {
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		l.Errorf("file not found for delete: file_id=%d err=%v", in.GetFileId(), err)
		return nil, ErrFileNotFound
	}
	if f.UploaderID != in.GetUserId() {
		return nil, ErrNotUploader
	}

	// best-effort delete from MinIO
	_ = l.svcCtx.MinIO.Delete(l.ctx, f.Key)

	if err := l.svcCtx.FileRepo.Delete(l.ctx, in.GetFileId()); err != nil {
		return nil, err
	}

	l.Infof("file deleted: file_id=%d", in.GetFileId())
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
