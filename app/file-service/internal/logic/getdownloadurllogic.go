package logic

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"

	"github.com/maomeng/aim/pkg/resourcegrant"
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
	if _, bot := resourcegrant.Incoming(l.ctx); !bot {
		if err := validateIdentities(in.GetFileId(), in.GetUserId()); err != nil {
			return nil, err
		}
	}
	f, err := l.svcCtx.FileRepo.GetByID(l.ctx, in.GetFileId())
	if err != nil {
		l.Errorf("file lookup failed for download URL: file_id=%s err=%v", in.GetFileId(), err)
		return nil, fileLookupError(err)
	}

	public := f.Access == int32(filepb.FileAccess_FILE_ACCESS_PUBLIC)
	if a, bot := resourcegrant.Incoming(l.ctx); bot {
		if _, err := resourcegrant.Check(l.svcCtx.DB.WithContext(l.ctx), a, "file", in.FileId, a.Visibility != "personal", false); err != nil {
			return nil, err
		}
	} else if !public && f.UploaderID != in.GetUserId() {
		if err := resourcegrant.ForViewer(l.svcCtx.DB.WithContext(l.ctx), in.GetUserId(), "file", in.FileId); err != nil {
			return nil, err
		}
	}
	if _, _, err := l.svcCtx.MinIO.Stat(l.ctx, f.Key); err != nil {
		return nil, fileLookupError(err)
	}

	var downloadURL string
	var expiresAt int64
	if public {
		downloadURL = l.svcCtx.MinIO.PublicURL(f.Key)
	} else {
		expiry := time.Duration(in.GetExpiresIn()) * time.Second
		if expiry <= 0 {
			expiry = time.Hour
		}
		downloadURL, err = l.svcCtx.MinIO.PresignedURL(l.ctx, f.Key, expiry)
		if err != nil {
			return nil, grpcError(err)
		}
		expiresAt = time.Now().Add(expiry).Unix()
	}

	l.Infof("download URL generated: file_id=%s", in.GetFileId())
	return &filepb.GetDownloadURLResp{
		DownloadUrl: downloadURL,
		ExpiresAt:   expiresAt,
		File:        toProtoFileInfo(f),
	}, nil
}
