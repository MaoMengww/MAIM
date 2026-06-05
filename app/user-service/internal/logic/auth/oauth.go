package auth

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

func (l *Logic) OAuthLogin(ctx context.Context, req *userpb.OAuthLoginReq) (*userpb.LoginResp, error) {
	if req.Provider == "" || req.Code == "" {
		return nil, errors.New(1001, "provider and code are required")
	}

	oauthID := fmt.Sprintf("%s_%s", req.Provider, req.Code)
	u, err := l.userRepo.GetByUsername(ctx, oauthID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(1010, "query oauth user failed", err)
	}
	if u == nil {
		id := l.snow.Generate()
		now := time.Now()
		u = &model.User{
			ID:        id,
			Username:  oauthID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := l.userRepo.Create(ctx, u); err != nil {
			return nil, errors.Wrap(1010, "create oauth user failed", err)
		}
	}

	userIDStr := strconv.FormatInt(u.ID, 10)
	accessToken, err := l.jwtMgr.Generate(userIDStr, u.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(userIDStr, u.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate refresh token failed", err)
	}

	if req.DeviceId != "" {
		device := &model.UserDevice{
			UserID:   u.ID,
			DeviceID: req.DeviceId,
			Platform: req.Platform,
		}
		_ = l.authRepo.SaveDevice(ctx, device)
	}

	nowUnix := time.Now().Unix()
	return &userpb.LoginResp{
		UserId: u.ID,
		Tokens: &userpb.TokenPair{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			AccessExpire:  nowUnix + 3600,
			RefreshExpire: nowUnix + 2592000,
		},
		User: modelToUserInfo(u),
	}, nil
}
