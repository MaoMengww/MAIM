package handler

import (
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"google.golang.org/grpc"
)

type FileHandler struct {
	fileClient filepb.FileServiceClient
	userClient userpb.UserServiceClient
}

func NewFileHandler(fileConn grpc.ClientConnInterface, userConn grpc.ClientConnInterface) *FileHandler {
	return &FileHandler{
		fileClient: filepb.NewFileServiceClient(fileConn),
		userClient: userpb.NewUserServiceClient(userConn),
	}
}

func (h *FileHandler) GetUploadURL(c *gin.Context) {
	var req filepb.GetUploadURLReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.UploaderId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.fileClient.GetUploadURL(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *FileHandler) UploadAvatar(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		response.BadRequest(c, "read file failed")
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	userID := c.GetString(middleware.CtxKeyUserID)
	req := &filepb.UploadAvatarReq{
		Data:     data,
		UserId:   userID,
		MimeType: mimeType,
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.fileClient.UploadAvatar(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 同步更新用户资料的 avatar 字段，确保刷新后和其他用户能看到新头像
	if resp.Url != "" {
		_, _ = h.userClient.UpdateProfile(ctx, &userpb.UpdateProfileReq{
			Avatar: &resp.Url,
		})
	}

	response.Success(c, resp)
}

func (h *FileHandler) ConfirmUpload(c *gin.Context) {
	var req filepb.ConfirmUploadReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.UploaderId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.fileClient.ConfirmUpload(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *FileHandler) GetDownloadURL(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &filepb.GetDownloadURLReq{
		FileId: c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.fileClient.GetDownloadURL(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *FileHandler) DeleteFile(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &filepb.DeleteFileReq{
		FileId: c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.fileClient.DeleteFile(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *FileHandler) GetFileInfo(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &filepb.GetFileInfoReq{
		FileId: c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.fileClient.GetFileInfo(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
