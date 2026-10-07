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
	if err := validateIdentities(in.GetFileId(), in.GetUserId()); err != nil {
		return nil, err
	}
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		l.Errorf("file lookup failed for delete: file_id=%s err=%v", in.GetFileId(), err)
		return nil, fileLookupError(err)
	}
	if f.UploaderID != in.GetUserId() {
		return nil, grpcError(ErrNotUploader)
	}

	if err := l.svcCtx.MinIO.Delete(l.ctx, f.Key); err != nil {
		return nil, grpcError(err)
	}

	if err := l.svcCtx.FileRepo.Delete(l.ctx, in.GetFileId()); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("file deleted: file_id=%s", in.GetFileId())
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
