package auth

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

func (l *Logic) GetSessions(ctx context.Context, userID int64) (*userpb.GetSessionsResp, error) {
	devices, err := l.authRepo.GetUserDevices(ctx, userID)
	if err != nil {
		logx.Errorf("get_sessions: get devices failed: user_id=%d, err=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get devices failed", err)
	}
	sessions := make([]*userpb.SessionInfo, 0, len(devices))
	for _, d := range devices {
		isOnline, err := l.authRepo.IsDeviceOnline(ctx, userID, d.DeviceID)
		if err != nil {
			logx.Errorf("get_sessions: check device online failed: user_id=%d, device_id=%s, err=%v", userID, d.DeviceID, err)
		}
		sessions = append(sessions, &userpb.SessionInfo{
			SessionId:    d.DeviceID,
			DeviceId:     d.DeviceID,
			Platform:     d.Platform,
			Ip:           d.IP,
			Location:     d.Location,
			LastActiveAt: d.LastActiveAt.Unix(),
			CreatedAt:    d.CreatedAt.Unix(),
			IsOnline:     isOnline,
		})
	}
	return &userpb.GetSessionsResp{Sessions: sessions}, nil
}

func (l *Logic) RevokeSession(ctx context.Context, userID int64, req *userpb.RevokeSessionReq) (*commonpb.BaseResponse, error) {
	if req.SessionId == "" {
		logx.Errorf("revoke_session: empty session_id")
		return nil, errors.New(errors.CodeInvalidParam, "session_id is required")
	}
	if err := l.authRepo.DeleteDevice(ctx, userID, req.SessionId); err != nil {
		logx.Errorf("revoke_session: revoke session failed: user_id=%d, session_id=%s, err=%v", userID, req.SessionId, err)
		return nil, errors.Wrap(errors.CodeDBError, "revoke session failed", err)
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}
