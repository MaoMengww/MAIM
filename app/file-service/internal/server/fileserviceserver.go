package server

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/logic"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type FileServiceServer struct {
	svcCtx *svc.ServiceContext
	filepb.UnimplementedFileServiceServer
}

func NewFileServiceServer(svcCtx *svc.ServiceContext) *FileServiceServer {
	return &FileServiceServer{svcCtx: svcCtx}
}

func (s *FileServiceServer) GetUploadURL(ctx context.Context, in *filepb.GetUploadURLReq) (*filepb.GetUploadURLResp, error) {
	if err := requireCaller(ctx, in.GetUploaderId()); err != nil {
		return nil, err
	}
	l := logic.NewGetUploadURLLogic(ctx, s.svcCtx)
	return l.GetUploadURL(in)
}

func (s *FileServiceServer) ConfirmUpload(ctx context.Context, in *filepb.ConfirmUploadReq) (*filepb.ConfirmUploadResp, error) {
	if err := requireCaller(ctx, in.GetUploaderId()); err != nil {
		return nil, err
	}
	l := logic.NewConfirmUploadLogic(ctx, s.svcCtx)
	return l.ConfirmUpload(in)
}

func (s *FileServiceServer) GetDownloadURL(ctx context.Context, in *filepb.GetDownloadURLReq) (*filepb.GetDownloadURLResp, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewGetDownloadURLLogic(ctx, s.svcCtx)
	return l.GetDownloadURL(in)
}

func (s *FileServiceServer) GetFileInfo(ctx context.Context, in *filepb.GetFileInfoReq) (*filepb.GetFileInfoResp, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewGetFileInfoLogic(ctx, s.svcCtx)
	return l.GetFileInfo(in)
}

func (s *FileServiceServer) BatchGetFileInfo(ctx context.Context, in *filepb.BatchGetFileInfoReq) (*filepb.BatchGetFileInfoResp, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewBatchGetFileInfoLogic(ctx, s.svcCtx)
	return l.BatchGetFileInfo(in)
}

func (s *FileServiceServer) DeleteFile(ctx context.Context, in *filepb.DeleteFileReq) (*common.BaseResponse, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewDeleteFileLogic(ctx, s.svcCtx)
	return l.DeleteFile(in)
}

func (s *FileServiceServer) BatchDeleteFiles(ctx context.Context, in *filepb.BatchDeleteFilesReq) (*common.BaseResponse, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewBatchDeleteFilesLogic(ctx, s.svcCtx)
	return l.BatchDeleteFiles(in)
}

func (s *FileServiceServer) UploadAvatar(ctx context.Context, in *filepb.UploadAvatarReq) (*filepb.UploadAvatarResp, error) {
	if err := requireCaller(ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	l := logic.NewUploadAvatarLogic(ctx, s.svcCtx)
	return l.UploadAvatar(in)
}

func requireCaller(ctx context.Context, requested string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	users := md.Get("user-id")
	if len(users) != 1 || identity.Validate(users[0]) != nil {
		return status.Error(codes.Unauthenticated, "missing or invalid user identity")
	}
	if identity.Validate(requested) != nil {
		return status.Error(codes.InvalidArgument, "invalid user identity")
	}
	if users[0] != requested {
		return status.Error(codes.PermissionDenied, "cannot act as another user")
	}
	return nil
}
