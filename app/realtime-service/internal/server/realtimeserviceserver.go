package server

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/maomeng/aim/app/realtime-service/internal/logic"
	"github.com/maomeng/aim/app/realtime-service/internal/svc"
	"github.com/maomeng/aim/app/realtime-service/pb/realtime"
	entityidentity "github.com/maomeng/aim/pkg/identity"
)

type RealtimeServiceServer struct {
	svcCtx *svc.ServiceContext
	realtime.UnimplementedRealtimeServiceServer
}

func NewRealtimeServiceServer(svcCtx *svc.ServiceContext) *RealtimeServiceServer {
	return &RealtimeServiceServer{svcCtx: svcCtx}
}

func (s *RealtimeServiceServer) ListNotifications(ctx context.Context, in *realtime.ListNotificationsReq) (*realtime.ListNotificationsResp, error) {
	l := logic.NewListNotificationsLogic(ctx, s.svcCtx)
	return l.ListNotifications(in)
}

func (s *RealtimeServiceServer) GetUnreadCount(ctx context.Context, in *realtime.GetUnreadCountReq) (*realtime.GetUnreadCountResp, error) {
	l := logic.NewGetUnreadCountLogic(ctx, s.svcCtx)
	return l.GetUnreadCount(in)
}

func (s *RealtimeServiceServer) MarkRead(ctx context.Context, in *realtime.MarkReadReq) (*realtime.MarkReadResp, error) {
	if err := entityidentity.Validate(in.NotificationId); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid notification identity")
	}
	l := logic.NewMarkReadLogic(ctx, s.svcCtx)
	return l.MarkRead(in)
}

func (s *RealtimeServiceServer) MarkAllRead(ctx context.Context, in *realtime.MarkAllReadReq) (*realtime.MarkAllReadResp, error) {
	l := logic.NewMarkAllReadLogic(ctx, s.svcCtx)
	return l.MarkAllRead(in)
}

func (s *RealtimeServiceServer) DeleteNotification(ctx context.Context, in *realtime.DeleteNotificationReq) (*realtime.DeleteNotificationResp, error) {
	if err := entityidentity.Validate(in.NotificationId); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid notification identity")
	}
	l := logic.NewDeleteNotificationLogic(ctx, s.svcCtx)
	return l.DeleteNotification(in)
}

func (s *RealtimeServiceServer) PushNotification(ctx context.Context, in *realtime.PushNotificationReq) (*realtime.PushNotificationResp, error) {
	for _, id := range in.UserIds {
		if err := entityidentity.Validate(id); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid recipient identity")
		}
	}
	if (in.ReferenceId == nil) != (in.ReferenceType == nil) {
		return nil, status.Error(codes.InvalidArgument, "reference identity and type must be provided together")
	}
	if in.ReferenceId != nil {
		if entityidentity.Validate(*in.ReferenceId) != nil || *in.ReferenceType == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid notification reference")
		}
	}
	l := logic.NewPushNotificationLogic(ctx, s.svcCtx)
	return l.PushNotification(in)
}

func (s *RealtimeServiceServer) IsOnline(ctx context.Context, in *realtime.IsOnlineReq) (*realtime.IsOnlineResp, error) {
	if err := entityidentity.Validate(in.UserId); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user identity")
	}
	l := logic.NewIsOnlineLogic(ctx, s.svcCtx)
	return l.IsOnline(in)
}

func (s *RealtimeServiceServer) BatchIsOnline(ctx context.Context, in *realtime.BatchIsOnlineReq) (*realtime.BatchIsOnlineResp, error) {
	for _, id := range in.UserIds {
		if err := entityidentity.Validate(id); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid user identity")
		}
	}
	l := logic.NewBatchIsOnlineLogic(ctx, s.svcCtx)
	return l.BatchIsOnline(in)
}

func identity(ctx context.Context) (string, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("user-id")
	if len(values) != 1 {
		return "", status.Error(codes.Unauthenticated, "missing user identity")
	}
	id := values[0]
	if err := entityidentity.Validate(id); err != nil {
		return "", status.Error(codes.Unauthenticated, "invalid user identity")
	}
	return id, nil
}
func (s *RealtimeServiceServer) RegisterDevice(ctx context.Context, in *realtime.RegisterDeviceReq) (*realtime.RegisterDeviceResp, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if in.DeviceId == "" || len(in.DeviceId) > 128 || in.Token == "" || len(in.Token) > 512 {
		return nil, status.Error(codes.InvalidArgument, "invalid device token")
	}
	if in.Platform != "ios" && in.Platform != "android" && in.Platform != "web" {
		return nil, status.Error(codes.InvalidArgument, "invalid platform")
	}
	provider := "fcm"
	if in.Platform == "ios" {
		provider = "apns"
	}
	err = s.svcCtx.PushService.RegisterDevice(ctx, id, in.DeviceId, in.Platform, in.Token, provider)
	return &realtime.RegisterDeviceResp{}, err
}
func (s *RealtimeServiceServer) UnregisterDevice(ctx context.Context, in *realtime.UnregisterDeviceReq) (*realtime.UnregisterDeviceResp, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if in.DeviceId == "" || len(in.DeviceId) > 128 {
		return nil, status.Error(codes.InvalidArgument, "invalid device")
	}
	return &realtime.UnregisterDeviceResp{}, s.svcCtx.PushService.UnregisterDevice(ctx, id, in.DeviceId)
}
