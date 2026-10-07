package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type BatchDeleteFilesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchDeleteFilesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchDeleteFilesLogic {
	return &BatchDeleteFilesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *BatchDeleteFilesLogic) BatchDeleteFiles(in *filepb.BatchDeleteFilesReq) (*common.BaseResponse, error) {
	if err := validateIdentities(in.GetUserId()); err != nil {
		return nil, err
	}
	if err := validateIdentities(in.GetFileIds()...); err != nil {
		return nil, err
	}
	ids := in.GetFileIds()
	if len(ids) == 0 {
		return &common.BaseResponse{Code: 0, Message: "ok"}, nil
	}

	files, err := l.svcCtx.FileRepo.BatchGetByIDs(l.ctx, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	var validIDs []string
	var storageErr error
	for _, f := range files {
		if f.UploaderID == in.GetUserId() {
			if err := l.svcCtx.MinIO.Delete(l.ctx, f.Key); err != nil {
				storageErr = err
				break
			}
			validIDs = append(validIDs, f.ID)
		}
	}

	if len(validIDs) > 0 {
		if err := l.svcCtx.FileRepo.BatchDelete(l.ctx, validIDs); err != nil {
			return nil, grpcError(err)
		}
	}
	if storageErr != nil {
		return nil, grpcError(storageErr)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
