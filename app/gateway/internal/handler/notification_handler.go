package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	signalpb "github.com/maomeng/aim/app/signaling-service/pb/signaling"
	"google.golang.org/grpc"
)

type NotificationHandler struct {
	notifClient signalpb.SignalingServiceClient
}

func NewNotificationHandler(conn grpc.ClientConnInterface) *NotificationHandler {
	return &NotificationHandler{notifClient: signalpb.NewSignalingServiceClient(conn)}
}

func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	var req signalpb.ListNotificationsReq
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.notifClient.ListNotifications(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	req := &signalpb.MarkReadReq{NotificationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.notifClient.MarkRead(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	var req signalpb.MarkAllReadReq
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.notifClient.MarkAllRead(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *NotificationHandler) DeleteNotification(c *gin.Context) {
	req := &signalpb.DeleteNotificationReq{NotificationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.notifClient.DeleteNotification(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
