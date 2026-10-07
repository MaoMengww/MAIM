package auth

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/metadata"
)

func (l *Logic) GetSessions(ctx context.Context, userID string) (*userpb.GetSessionsResp, error) {
	if err := sessionOwner(ctx, userID); err != nil {
		return nil, err
	}
	devices, err := l.authRepo.GetUserDevices(ctx, userID)
	if err != nil {
		logx.Errorf("get_sessions: get devices failed: user_id=%s, err=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get devices failed", err)
	}
	sessions := make([]*userpb.SessionInfo, 0, len(devices))
	md, _ := metadata.FromIncomingContext(ctx)
	currentDevice := md.Get("device-id")
	for _, d := range devices {
		isOnline, err := l.authRepo.IsDeviceOnline(ctx, userID, d.DeviceID)
		if err != nil {
			logx.Errorf("get_sessions: check device online failed: user_id=%s, device_id=%s, err=%v", userID, d.DeviceID, err)
		}
		sessions = append(sessions, &userpb.SessionInfo{
			SessionId:    d.ID,
			DeviceId:     d.DeviceID,
			Platform:     d.Platform,
			Ip:           d.IP,
			Location:     d.Location,
			LastActiveAt: d.LastActiveAt.Unix(),
			CreatedAt:    d.CreatedAt.Unix(),
			IsOnline:     isOnline,
			IsCurrent:    len(currentDevice) == 1 && currentDevice[0] == d.DeviceID,
		})
	}
	return &userpb.GetSessionsResp{Sessions: sessions}, nil
}

func (l *Logic) RevokeSession(ctx context.Context, userID string, req *userpb.RevokeSessionReq) (*commonpb.BaseResponse, error) {
	if err := sessionOwner(ctx, userID); err != nil {
		return nil, err
	}
	if identity.Validate(req.SessionId) != nil {
		logx.Errorf("revoke_session: empty session_id")
		return nil, errors.New(errors.CodeInvalidParam, "session_id is required")
	}
	if req.UserId != "" && req.UserId != userID {
		return nil, errors.New(errors.CodeForbidden, "session belongs to another account")
	}
	devices, err := l.authRepo.GetUserDevices(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get devices failed", err)
	}
	var deviceID string
	for _, device := range devices {
		if device.ID == req.SessionId {
			deviceID = device.DeviceID
			break
		}
	}
	if deviceID == "" {
		return nil, errors.New(errors.CodeForbidden, "session is not owned by account")
	}
	if err := l.authRepo.DeleteSession(ctx, userID, req.SessionId); err != nil {
		logx.Errorf("revoke_session: revoke session failed: user_id=%s, session_id=%s, err=%v", userID, req.SessionId, err)
		return nil, errors.Wrap(errors.CodeDBError, "revoke session failed", err)
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}
func sessionOwner(ctx context.Context, userID string) error {
	owner, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(owner) != nil {
		return errors.New(errors.CodeUnauthorized, "authentication required")
	}
	if userID != owner {
		return errors.New(errors.CodeForbidden, "account is not owned by caller")
	}
	return nil
}
