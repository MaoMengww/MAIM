package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	realtimepb "github.com/maomeng/aim/app/realtime-service/pb/realtime"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"strconv"
)

type RealtimeHandler struct {
	notifClient realtimepb.RealtimeServiceClient
}

func NewRealtimeHandler(conn grpc.ClientConnInterface) *RealtimeHandler {
	return &RealtimeHandler{notifClient: realtimepb.NewRealtimeServiceClient(conn)}
}

func (h *RealtimeHandler) ListNotifications(c *gin.Context) {
	var req realtimepb.ListNotificationsReq
	req.Pagination = &common.Pagination{Page: 1, PageSize: 20}
	for name, target := range map[string]*int32{"page": &req.Pagination.Page, "page_size": &req.Pagination.PageSize} {
		if raw, supplied := c.GetQuery(name); supplied {
			value, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || value < 1 || (name == "page_size" && value > 100) {
				response.BadRequest(c, "invalid "+name)
				return
			}
			*target = int32(value)
		}
	}
	if raw, supplied := c.GetQuery("type"); supplied {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			response.BadRequest(c, "invalid notification type")
			return
		}
		notifType := int32(value)
		req.Type = &notifType
	}
	if raw, supplied := c.GetQuery("is_read"); supplied {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "invalid is_read")
			return
		}
		req.IsRead = &value
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.notifClient.ListNotifications(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *RealtimeHandler) GetUnreadCount(c *gin.Context) {
	resp, err := h.notifClient.GetUnreadCount(middleware.WithGRPCMetadata(c), &realtimepb.GetUnreadCountReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"count": resp.Count})
}

func (h *RealtimeHandler) MarkRead(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &realtimepb.MarkReadReq{NotificationId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.notifClient.MarkRead(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *RealtimeHandler) MarkAllRead(c *gin.Context) {
	var req realtimepb.MarkAllReadReq
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.notifClient.MarkAllRead(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *RealtimeHandler) DeleteNotification(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &realtimepb.DeleteNotificationReq{NotificationId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.notifClient.DeleteNotification(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *RealtimeHandler) RegisterDevice(c *gin.Context) {
	var body struct {
		DeviceID string `json:"device_id"`
		Platform string `json:"platform"`
		Token    string `json:"token"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, "invalid device token")
		return
	}
	resp, err := h.notifClient.RegisterDevice(middleware.WithGRPCMetadata(c), &realtimepb.RegisterDeviceReq{DeviceId: body.DeviceID, Platform: body.Platform, Token: body.Token})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
func (h *RealtimeHandler) UnregisterDevice(c *gin.Context) {
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, "invalid device")
		return
	}
	resp, err := h.notifClient.UnregisterDevice(middleware.WithGRPCMetadata(c), &realtimepb.UnregisterDeviceReq{DeviceId: body.DeviceID})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
