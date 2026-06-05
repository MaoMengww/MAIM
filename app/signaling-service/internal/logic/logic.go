package logic

import (
	"context"
	"math/rand"
	"time"

	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"github.com/maomeng/aim/app/signaling-service/internal/svc"
	"github.com/maomeng/aim/app/signaling-service/pb/signaling"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
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

func (l *ListNotificationsLogic) ListNotifications(in *signaling.ListNotificationsReq) (*signaling.ListNotificationsResp, error) {
	page := int(in.Pagination.GetPage())
	if page <= 0 {
		page = 1
	}
	pageSize := int(in.Pagination.GetPageSize())
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	notifs, total, err := l.svcCtx.NotifRepo.List(l.ctx, 0, in.Type, in.IsRead, page, pageSize)
	if err != nil {
		return nil, err
	}
	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) > 0 {
		totalPages++
	}
	pbNotifs := make([]*signaling.Notification, len(notifs))
	for i, n := range notifs {
		pbNotifs[i] = toPB(n)
	}
	return &signaling.ListNotificationsResp{
		Notifications: pbNotifs,
		Pagination:    &common.PaginationResp{Page: int32(page), PageSize: int32(pageSize), Total: total, TotalPages: totalPages},
	}, nil
}

// ── GetUnreadCount ──

type GetUnreadCountLogic struct{ BaseLogic }

func NewGetUnreadCountLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetUnreadCountLogic {
	return &GetUnreadCountLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *GetUnreadCountLogic) GetUnreadCount(in *signaling.GetUnreadCountReq) (*signaling.GetUnreadCountResp, error) {
	count, err := l.svcCtx.NotifRepo.UnreadCount(l.ctx, 0)
	if err != nil {
		return nil, err
	}
	return &signaling.GetUnreadCountResp{Count: count}, nil
}

// ── MarkRead ──

type MarkReadLogic struct{ BaseLogic }

func NewMarkReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkReadLogic {
	return &MarkReadLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *MarkReadLogic) MarkRead(in *signaling.MarkReadReq) (*signaling.MarkReadResp, error) {
	return &signaling.MarkReadResp{}, l.svcCtx.NotifRepo.MarkRead(l.ctx, 0, in.NotificationId)
}

// ── MarkAllRead ──

type MarkAllReadLogic struct{ BaseLogic }

func NewMarkAllReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkAllReadLogic {
	return &MarkAllReadLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *MarkAllReadLogic) MarkAllRead(in *signaling.MarkAllReadReq) (*signaling.MarkAllReadResp, error) {
	return &signaling.MarkAllReadResp{}, l.svcCtx.NotifRepo.MarkAllRead(l.ctx, 0)
}

// ── DeleteNotification ──

type DeleteNotificationLogic struct{ BaseLogic }

func NewDeleteNotificationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteNotificationLogic {
	return &DeleteNotificationLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *DeleteNotificationLogic) DeleteNotification(in *signaling.DeleteNotificationReq) (*signaling.DeleteNotificationResp, error) {
	return &signaling.DeleteNotificationResp{}, l.svcCtx.NotifRepo.Delete(l.ctx, 0, in.NotificationId)
}

// ── PushNotification ──

type PushNotificationLogic struct{ BaseLogic }

func NewPushNotificationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PushNotificationLogic {
	return &PushNotificationLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *PushNotificationLogic) PushNotification(in *signaling.PushNotificationReq) (*signaling.PushNotificationResp, error) {
	now := time.Now().UnixMilli()
	firstID := int64(0)
	for i, uid := range in.UserIds {
		n := &model.Notification{
			ID: now*1000 + int64(rand.Intn(999)), UserID: uid, Type: in.Type,
			Title: in.Title, Content: in.Content, IsRead: false,
			ReferenceID: in.ReferenceId, CreatedAt: now / 1000,
		}
		if err := l.svcCtx.NotifRepo.Create(l.ctx, n); err != nil {
			return nil, err
		}
		if i == 0 {
			firstID = n.ID
		}
	}
	return &signaling.PushNotificationResp{FirstNotificationId: firstID}, nil
}

// ── IsOnline ──

type IsOnlineLogic struct{ BaseLogic }

func NewIsOnlineLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IsOnlineLogic {
	return &IsOnlineLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *IsOnlineLogic) IsOnline(in *signaling.IsOnlineReq) (*signaling.IsOnlineResp, error) {
	online := l.svcCtx.PresenceChecker.IsOnline(l.ctx, in.UserId)
	return &signaling.IsOnlineResp{Online: online}, nil
}

// ── BatchIsOnline ──

type BatchIsOnlineLogic struct{ BaseLogic }

func NewBatchIsOnlineLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchIsOnlineLogic {
	return &BatchIsOnlineLogic{BaseLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}}
}

func (l *BatchIsOnlineLogic) BatchIsOnline(in *signaling.BatchIsOnlineReq) (*signaling.BatchIsOnlineResp, error) {
	status := make(map[int64]bool, len(in.UserIds))
	for _, uid := range in.UserIds {
		status[uid] = l.svcCtx.PresenceChecker.IsOnline(l.ctx, uid)
	}
	return &signaling.BatchIsOnlineResp{Status: status}, nil
}

func toPB(n model.Notification) *signaling.Notification {
	return &signaling.Notification{
		Id: n.ID, UserId: n.UserID, Type: n.Type, Title: n.Title,
		Content: n.Content, IsRead: n.IsRead, ReferenceId: n.ReferenceID, CreatedAt: n.CreatedAt,
	}
}
