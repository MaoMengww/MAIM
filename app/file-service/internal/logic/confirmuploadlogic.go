package logic

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/metrics"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/zeromicro/go-zero/core/logx"
)

type ConfirmUploadLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewConfirmUploadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ConfirmUploadLogic {
	return &ConfirmUploadLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ConfirmUploadLogic) ConfirmUpload(in *filepb.ConfirmUploadReq) (*filepb.ConfirmUploadResp, error) {
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		l.Errorf("file not found for confirm upload: file_id=%d err=%v", in.GetFileId(), err)
		return nil, grpcError(ErrFileNotFound)
	}
	if f.UploaderID != in.GetUploaderId() {
		return nil, grpcError(ErrNotUploader)
	}

	if in.Md5 != nil && in.GetMd5() != "" {
		md5sum := in.GetMd5()
		if f.Md5 != "" && f.Md5 != md5sum {
			return nil, grpcError(ErrMD5Mismatch)
		}
		if f.Md5 == "" {
			f.Md5 = md5sum
			if err := l.svcCtx.FileRepo.Update(l.ctx, f); err != nil {
				return nil, grpcError(err)
			}
		}
	}

	category := metrics.PurposeCategory(f.Purpose)
	metrics.FileUploadTotal.Inc(category)
	metrics.FileStorageBytes.Add(float64(f.Size), category)

	l.Infof("upload confirmed: file_id=%d size=%d category=%s", in.GetFileId(), f.Size, category)
	return &filepb.ConfirmUploadResp{File: toProtoFileInfo(f)}, nil
}
