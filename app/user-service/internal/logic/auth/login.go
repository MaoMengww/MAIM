package auth

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/metrics"
	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func (l *Logic) Login(ctx context.Context, req *userpb.LoginReq) (*userpb.LoginResp, error) {
	if req.DeviceId == "" || len(req.DeviceId) > 128 {
		return nil, errors.New(errors.CodeInvalidParam, "device_id is required")
	}
	logger := l.ctxLogger(ctx)

	if req.Account == "" || req.Password == "" {
		err := errors.New(errors.CodeInvalidParam, "account and password are required")
		logger.Errorf("method=Login error=%v", err)
		return nil, err
	}

	var u *model.User
	var err error
	u, err = l.userRepo.GetByUsername(ctx, req.Account)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(errors.CodeDBError, "query user failed", err)
	}
	if u == nil {
		u, err = l.userRepo.GetByPhone(ctx, req.Account)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(errors.CodeDBError, "query user failed", err)
		}
	}
	if u == nil {
		u, err = l.userRepo.GetByEmail(ctx, req.Account)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(errors.CodeDBError, "query user failed", err)
		}
	}
	if u == nil {
		err := errors.New(errors.CodeNotFound, "user not found")
		logger.Errorf("method=Login error=%v", err)
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		logger.Errorf("method=Login user_id=%s error=invalid password", u.ID)
		return nil, errors.New(errors.CodeUnauthorized, "invalid password")
	}

	metrics.UserLoginsTotal.Inc("password")

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
	logger.Infof("method=Login user_id=%s username=%s", u.ID, u.Username)
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
