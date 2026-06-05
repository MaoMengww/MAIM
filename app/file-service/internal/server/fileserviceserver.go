package server

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/logic"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/pb/common"
)

type FileServiceServer struct {
	svcCtx *svc.ServiceContext
	filepb.UnimplementedFileServiceServer
}

func NewFileServiceServer(svcCtx *svc.ServiceContext) *FileServiceServer {
	return &FileServiceServer{svcCtx: svcCtx}
}

func (s *FileServiceServer) GetUploadURL(ctx context.Context, in *filepb.GetUploadURLReq) (*filepb.GetUploadURLResp, error) {
	l := logic.NewGetUploadURLLogic(ctx, s.svcCtx)
	return l.GetUploadURL(in)
}

func (s *FileServiceServer) ConfirmUpload(ctx context.Context, in *filepb.ConfirmUploadReq) (*filepb.ConfirmUploadResp, error) {
	l := logic.NewConfirmUploadLogic(ctx, s.svcCtx)
	return l.ConfirmUpload(in)
}

func (s *FileServiceServer) GetDownloadURL(ctx context.Context, in *filepb.GetDownloadURLReq) (*filepb.GetDownloadURLResp, error) {
	l := logic.NewGetDownloadURLLogic(ctx, s.svcCtx)
	return l.GetDownloadURL(in)
}

func (s *FileServiceServer) GetFileInfo(ctx context.Context, in *filepb.GetFileInfoReq) (*filepb.GetFileInfoResp, error) {
	l := logic.NewGetFileInfoLogic(ctx, s.svcCtx)
	return l.GetFileInfo(in)
}

func (s *FileServiceServer) BatchGetFileInfo(ctx context.Context, in *filepb.BatchGetFileInfoReq) (*filepb.BatchGetFileInfoResp, error) {
	l := logic.NewBatchGetFileInfoLogic(ctx, s.svcCtx)
	return l.BatchGetFileInfo(in)
}

func (s *FileServiceServer) DeleteFile(ctx context.Context, in *filepb.DeleteFileReq) (*common.BaseResponse, error) {
	l := logic.NewDeleteFileLogic(ctx, s.svcCtx)
	return l.DeleteFile(in)
}

func (s *FileServiceServer) BatchDeleteFiles(ctx context.Context, in *filepb.BatchDeleteFilesReq) (*common.BaseResponse, error) {
	l := logic.NewBatchDeleteFilesLogic(ctx, s.svcCtx)
	return l.BatchDeleteFiles(in)
}

func (s *FileServiceServer) UploadAvatar(ctx context.Context, in *filepb.UploadAvatarReq) (*filepb.UploadAvatarResp, error) {
	l := logic.NewUploadAvatarLogic(ctx, s.svcCtx)
	return l.UploadAvatar(in)
}
