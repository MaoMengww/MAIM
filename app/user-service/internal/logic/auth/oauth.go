package auth

import (
	"context"
	"fmt"
	"github.com/maomeng/aim/pkg/identity"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

func (l *Logic) OAuthLogin(ctx context.Context, req *userpb.OAuthLoginReq) (*userpb.LoginResp, error) {
	if req.DeviceId == "" || len(req.DeviceId) > 128 {
		return nil, errors.New(errors.CodeInvalidParam, "device_id is required")
	}
	if req.Provider == "" || req.Code == "" {
		return nil, errors.New(errors.CodeInvalidParam, "provider and code are required")
	}

	oauthID := fmt.Sprintf("%s_%s", req.Provider, req.Code)
	u, err := l.userRepo.GetByUsername(ctx, oauthID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(errors.CodeDBError, "query oauth user failed", err)
	}
	if u == nil {
		id, err := identity.New()
		if err != nil {
			return nil, fmt.Errorf("generate user id failed: %w", err)
		}
		now := time.Now()
		u = &model.User{
			ID:        id,
			Username:  oauthID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := l.userRepo.Create(ctx, u); err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "create oauth user failed", err)
		}
	}

	if err := l.authRepo.SaveDevice(ctx, &model.UserDevice{UserID: u.ID, DeviceID: req.DeviceId, Platform: req.Platform}); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "save device failed", err)
	}
	device, err := l.authRepo.GetDevice(ctx, u.ID, req.DeviceId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get device failed", err)
	}
	accessToken, err := l.jwtMgr.Generate(u.ID, u.Username, req.DeviceId, device.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(u.ID, u.Username, req.DeviceId, device.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate refresh token failed", err)
	}

	nowUnix := time.Now().Unix()
	return &userpb.LoginResp{
		UserId: u.ID,
		Tokens: &userpb.TokenPair{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			AccessExpire:  nowUnix + int64(l.jwtMgr.ExpireSeconds()),
			RefreshExpire: nowUnix + int64(l.jwtMgr.ExpireSeconds()),
		},
		User: modelToUserInfo(u),
	}, nil
}
