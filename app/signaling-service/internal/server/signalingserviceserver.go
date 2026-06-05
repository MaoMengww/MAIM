package server

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/signaling-service/internal/logic"
	"github.com/maomeng/aim/app/signaling-service/internal/svc"
	"github.com/maomeng/aim/app/signaling-service/pb/signaling"
)

type SignalingServiceServer struct {
	svcCtx *svc.ServiceContext
	signaling.UnimplementedSignalingServiceServer
}

func NewSignalingServiceServer(svcCtx *svc.ServiceContext) *SignalingServiceServer {
	return &SignalingServiceServer{svcCtx: svcCtx}
}

func (s *SignalingServiceServer) ListNotifications(ctx context.Context, in *signaling.ListNotificationsReq) (*signaling.ListNotificationsResp, error) {
	l := logic.NewListNotificationsLogic(ctx, s.svcCtx)
	return l.ListNotifications(in)
}

func (s *SignalingServiceServer) GetUnreadCount(ctx context.Context, in *signaling.GetUnreadCountReq) (*signaling.GetUnreadCountResp, error) {
	l := logic.NewGetUnreadCountLogic(ctx, s.svcCtx)
	return l.GetUnreadCount(in)
}

func (s *SignalingServiceServer) MarkRead(ctx context.Context, in *signaling.MarkReadReq) (*signaling.MarkReadResp, error) {
	l := logic.NewMarkReadLogic(ctx, s.svcCtx)
	return l.MarkRead(in)
}

func (s *SignalingServiceServer) MarkAllRead(ctx context.Context, in *signaling.MarkAllReadReq) (*signaling.MarkAllReadResp, error) {
	l := logic.NewMarkAllReadLogic(ctx, s.svcCtx)
	return l.MarkAllRead(in)
}

func (s *SignalingServiceServer) DeleteNotification(ctx context.Context, in *signaling.DeleteNotificationReq) (*signaling.DeleteNotificationResp, error) {
	l := logic.NewDeleteNotificationLogic(ctx, s.svcCtx)
	return l.DeleteNotification(in)
}

func (s *SignalingServiceServer) PushNotification(ctx context.Context, in *signaling.PushNotificationReq) (*signaling.PushNotificationResp, error) {
	l := logic.NewPushNotificationLogic(ctx, s.svcCtx)
	return l.PushNotification(in)
}

func (s *SignalingServiceServer) IsOnline(ctx context.Context, in *signaling.IsOnlineReq) (*signaling.IsOnlineResp, error) {
	l := logic.NewIsOnlineLogic(ctx, s.svcCtx)
	return l.IsOnline(in)
}

func (s *SignalingServiceServer) BatchIsOnline(ctx context.Context, in *signaling.BatchIsOnlineReq) (*signaling.BatchIsOnlineResp, error) {
	l := logic.NewBatchIsOnlineLogic(ctx, s.svcCtx)
	return l.BatchIsOnline(in)
}

func _() {
	_ = strconv.Itoa // keep import alive
}
