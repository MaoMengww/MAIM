package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type BatchGetFileInfoLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchGetFileInfoLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchGetFileInfoLogic {
	return &BatchGetFileInfoLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *BatchGetFileInfoLogic) BatchGetFileInfo(in *filepb.BatchGetFileInfoReq) (*filepb.BatchGetFileInfoResp, error) {
	if len(in.GetFileIds()) == 0 {
		return &filepb.BatchGetFileInfoResp{Files: []*filepb.FileInfo{}}, nil
	}

	files, err := l.svcCtx.FileRepo.BatchGetByIDs(l.ctx, in.GetFileIds())
	if err != nil {
		return nil, err
	}

	items := make([]*filepb.FileInfo, 0, len(files))
	for _, f := range files {
		// Skip private files not owned by requester
		if f.Access != int32(filepb.FileAccess_FILE_ACCESS_PUBLIC) && f.UploaderID != in.GetUserId() {
			continue
		}
		items = append(items, toProtoFileInfo(&f))
	}

	return &filepb.BatchGetFileInfoResp{Files: items}, nil
}
