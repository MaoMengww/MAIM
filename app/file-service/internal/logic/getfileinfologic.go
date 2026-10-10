package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/maomeng/aim/pkg/resourcegrant"
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
	if _, bot := resourcegrant.Incoming(l.ctx); !bot {
		if err := validateIdentities(in.GetFileId(), in.GetUserId()); err != nil {
			return nil, err
		}
	}
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		return nil, fileLookupError(err)
	}

	// Check access
	if a, bot := resourcegrant.Incoming(l.ctx); bot {
		if _, err := resourcegrant.Check(l.svcCtx.DB.WithContext(l.ctx), a, "file", in.FileId, a.Visibility != "personal", false); err != nil {
			return nil, err
		}
	} else if f.Access != int32(filepb.FileAccess_FILE_ACCESS_PUBLIC) && f.UploaderID != in.GetUserId() {
		if err := resourcegrant.ForViewer(l.svcCtx.DB.WithContext(l.ctx), in.GetUserId(), "file", in.FileId); err != nil {
			return nil, err
		}
	}

	return &filepb.GetFileInfoResp{File: toProtoFileInfo(f)}, nil
}
