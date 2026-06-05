package auth

import (
	"context"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/metrics"
	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func (l *Logic) Login(ctx context.Context, req *userpb.LoginReq) (*userpb.LoginResp, error) {
	logger := l.ctxLogger(ctx)

	if req.Account == "" || req.Password == "" {
		err := errors.New(1001, "account and password are required")
		logger.Errorf("method=Login error=%v", err)
		return nil, err
	}

	var u *model.User
	var err error
	u, err = l.userRepo.GetByUsername(ctx, req.Account)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(1010, "query user failed", err)
	}
	if u == nil {
		u, err = l.userRepo.GetByPhone(ctx, req.Account)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(1010, "query user failed", err)
		}
	}
	if u == nil {
		u, err = l.userRepo.GetByEmail(ctx, req.Account)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(1010, "query user failed", err)
		}
	}
	if u == nil {
		err := errors.New(1004, "user not found")
		logger.Errorf("method=Login error=%v", err)
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		logger.Errorf("method=Login user_id=%d error=invalid password", u.ID)
		return nil, errors.New(1002, "invalid password")
	}

	metrics.UserLoginsTotal.Inc("password")

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
	logger.Infof("method=Login user_id=%d username=%s", u.ID, u.Username)
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
