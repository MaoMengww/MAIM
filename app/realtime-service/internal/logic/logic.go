package logic

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/realtime-service/internal/model"
	"github.com/maomeng/aim/app/realtime-service/internal/svc"
	"github.com/maomeng/aim/app/realtime-service/pb/realtime"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/metadata"
)

type BaseLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

// ── ListNotifications ──

type ListNotificationsLogic struct{ BaseLogic }

func NewListNotificationsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListNotificationsLogic {
	return &ListNotificationsLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *ListNotificationsLogic) ListNotifications(in *realtime.ListNotificationsReq) (*realtime.ListNotificationsResp, error) {
	page := int(in.Pagination.GetPage())
	if page <= 0 {
		page = 1
	}
	pageSize := int(in.Pagination.GetPageSize())
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	notifs, total, err := l.svcCtx.NotifRepo.List(l.ctx, userID(l.ctx), in.Type, in.IsRead, page, pageSize)
	if err != nil {
		return nil, err
	}
	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) > 0 {
		totalPages++
	}
	pbNotifs := make([]*realtime.Notification, len(notifs))
	for i, n := range notifs {
		pbNotifs[i] = toPB(n)
	}
	return &realtime.ListNotificationsResp{
		Notifications: pbNotifs,
		Pagination:    &common.PaginationResp{Page: int32(page), PageSize: int32(pageSize), Total: total, TotalPages: totalPages},
	}, nil
}

// ── GetUnreadCount ──

type GetUnreadCountLogic struct{ BaseLogic }

func NewGetUnreadCountLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetUnreadCountLogic {
	return &GetUnreadCountLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *GetUnreadCountLogic) GetUnreadCount(in *realtime.GetUnreadCountReq) (*realtime.GetUnreadCountResp, error) {
	count, err := l.svcCtx.NotifRepo.UnreadCount(l.ctx, userID(l.ctx))
	if err != nil {
		return nil, err
	}
	return &realtime.GetUnreadCountResp{Count: count}, nil
}

// ── MarkRead ──

type MarkReadLogic struct{ BaseLogic }

func NewMarkReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkReadLogic {
	return &MarkReadLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *MarkReadLogic) MarkRead(in *realtime.MarkReadReq) (*realtime.MarkReadResp, error) {
	return &realtime.MarkReadResp{}, l.svcCtx.NotifRepo.MarkRead(l.ctx, userID(l.ctx), in.NotificationId)
}

// ── MarkAllRead ──

type MarkAllReadLogic struct{ BaseLogic }

func NewMarkAllReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkAllReadLogic {
	return &MarkAllReadLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *MarkAllReadLogic) MarkAllRead(in *realtime.MarkAllReadReq) (*realtime.MarkAllReadResp, error) {
	return &realtime.MarkAllReadResp{}, l.svcCtx.NotifRepo.MarkAllRead(l.ctx, userID(l.ctx))
}

// ── DeleteNotification ──

type DeleteNotificationLogic struct{ BaseLogic }

func NewDeleteNotificationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteNotificationLogic {
	return &DeleteNotificationLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *DeleteNotificationLogic) DeleteNotification(in *realtime.DeleteNotificationReq) (*realtime.DeleteNotificationResp, error) {
	return &realtime.DeleteNotificationResp{}, l.svcCtx.NotifRepo.Delete(l.ctx, userID(l.ctx), in.NotificationId)
}

// ── PushNotification ──

type PushNotificationLogic struct{ BaseLogic }

func NewPushNotificationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PushNotificationLogic {
	return &PushNotificationLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *PushNotificationLogic) PushNotification(in *realtime.PushNotificationReq) (*realtime.PushNotificationResp, error) {
	now := time.Now().UnixMilli()
	firstID := int64(0)
	for i, uid := range in.UserIds {
		n := &model.Notification{
			UserID: uid, Type: in.Type,
			Title: in.Title, Content: in.Content, IsRead: false,
			ReferenceID: in.ReferenceId, CreatedAt: now / 1000,
		}
		if err := l.svcCtx.NotifRepo.Create(l.ctx, n); err != nil {
			return nil, err
		}
		if i == 0 {
			firstID = n.ID
		}
		payload, err := json.Marshal(map[string]any{"type": consts.EventNotificationNew, "notification": toPB(*n)})
		if err != nil {
			return nil, err
		}
		if err := l.svcCtx.Router.Deliver(l.ctx, delivery.Intent{UserIDs: []int64{uid}, Payload: payload, Notification: &delivery.Notification{Title: in.Title, Body: in.Content}}); err != nil {
			l.Errorf("notification delivery: %v", err)
		}
	}
	return &realtime.PushNotificationResp{FirstNotificationId: firstID}, nil
}

// ── IsOnline ──

type IsOnlineLogic struct{ BaseLogic }

func NewIsOnlineLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IsOnlineLogic {
	return &IsOnlineLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *IsOnlineLogic) IsOnline(in *realtime.IsOnlineReq) (*realtime.IsOnlineResp, error) {
	online := l.svcCtx.PresenceChecker.IsOnline(l.ctx, in.UserId)
	return &realtime.IsOnlineResp{Online: online}, nil
}

// ── BatchIsOnline ──

type BatchIsOnlineLogic struct{ BaseLogic }

func NewBatchIsOnlineLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchIsOnlineLogic {
	return &BatchIsOnlineLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *BatchIsOnlineLogic) BatchIsOnline(in *realtime.BatchIsOnlineReq) (*realtime.BatchIsOnlineResp, error) {
	status := make(map[int64]bool, len(in.UserIds))
	for _, uid := range in.UserIds {
		status[uid] = l.svcCtx.PresenceChecker.IsOnline(l.ctx, uid)
	}
	return &realtime.BatchIsOnlineResp{Status: status}, nil
}

func toPB(n model.Notification) *realtime.Notification {
	return &realtime.Notification{
		Id: n.ID, UserId: n.UserID, Type: n.Type, Title: n.Title,
		Content: n.Content, IsRead: n.IsRead, ReferenceId: n.ReferenceID, CreatedAt: n.CreatedAt,
	}
}

func userID(ctx context.Context) int64 {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("user-id")
	if len(values) != 1 {
		return 0
	}
	id, _ := strconv.ParseInt(values[0], 10, 64)
	return id
}
