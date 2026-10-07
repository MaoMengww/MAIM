package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/metrics"
	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserRepo defines user repository methods needed by this package.
type UserRepo interface {
	Create(ctx context.Context, user *model.User) error
	GetByID(ctx context.Context, id string) (*model.User, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	GetByPhone(ctx context.Context, phone string) (*model.User, error)
	GetByEmail(ctx context.Context, email string) (*model.User, error)
}

// AuthRepo defines auth repository methods needed by this package.
type AuthRepo interface {
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	SaveDevice(ctx context.Context, d *model.UserDevice) error
	GetUserDevices(ctx context.Context, userID string) ([]*model.UserDevice, error)
	DeleteSession(ctx context.Context, userID string, sessionID string) error
	IsDeviceOnline(ctx context.Context, userID string, deviceID string) (bool, error)
	GetDevice(ctx context.Context, userID string, deviceID string) (*model.UserDevice, error)
}

type Logic struct {
	userRepo UserRepo
	authRepo AuthRepo
	jwtMgr   *jwt.Manager
	logger   logx.Logger
}

func New(userRepo UserRepo, authRepo AuthRepo, jwtMgr *jwt.Manager, logger logx.Logger) *Logic {
	return &Logic{userRepo: userRepo, authRepo: authRepo, jwtMgr: jwtMgr, logger: logger}
}

func (l *Logic) ctxLogger(ctx context.Context) logx.Logger {
	if l.logger != nil {
		return l.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
}

func (l *Logic) Register(ctx context.Context, req *userpb.RegisterReq) (*userpb.RegisterResp, error) {
	logger := l.ctxLogger(ctx)
	if req.DeviceId == "" || len(req.DeviceId) > 128 {
		return nil, errors.New(errors.CodeInvalidParam, "device_id is required")
	}

	if req.Username == "" || req.Password == "" {
		err := errors.New(errors.CodeInvalidParam, "username and password are required")
		logger.Errorf("method=Register error=%v", err)
		return nil, err
	}

	exist, err := l.userRepo.GetByUsername(ctx, req.Username)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(errors.CodeDBError, "check username failed", err)
	}
	if exist != nil {
		err := errors.New(errors.CodeConflict, "username already exists")
		logger.Errorf("method=Register error=%v", err)
		return nil, err
	}

	if req.Phone != "" {
		exist, err = l.userRepo.GetByPhone(ctx, req.Phone)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(errors.CodeDBError, "check phone failed", err)
		}
		if exist != nil {
			err := errors.New(errors.CodeConflict, "phone already registered")
			logger.Errorf("method=Register error=%v", err)
			return nil, err
		}
	}
	if req.Email != "" {
		exist, err = l.userRepo.GetByEmail(ctx, req.Email)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, errors.Wrap(errors.CodeDBError, "check email failed", err)
		}
		if exist != nil {
			err := errors.New(errors.CodeConflict, "email already registered")
			logger.Errorf("method=Register error=%v", err)
			return nil, err
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "hash password failed", err)
	}

	id, err := identity.New()
	if err != nil {
		return nil, fmt.Errorf("generate user id failed: %w", err)
	}
	now := time.Now()
	user := &model.User{
		ID:           id,
		Username:     req.Username,
		PasswordHash: string(hash),
		Phone:        req.Phone,
		Email:        req.Email,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := l.userRepo.Create(ctx, user); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "create user failed", err)
	}

	source := "username"
	if req.Email != "" {
		source = "email"
	} else if req.Phone != "" {
		source = "phone"
	}
	metrics.UserRegistrationsTotal.Inc(source)

	if err := l.authRepo.SaveDevice(ctx, &model.UserDevice{UserID: id, DeviceID: req.DeviceId, Platform: req.Platform}); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "save device failed", err)
	}
	device, err := l.authRepo.GetDevice(ctx, id, req.DeviceId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get device failed", err)
	}
	accessToken, err := l.jwtMgr.Generate(id, req.Username, req.DeviceId, device.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(id, req.Username, req.DeviceId, device.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate refresh token failed", err)
	}

	nowUnix := time.Now().Unix()
	logger.Infof("method=Register user_id=%s username=%s", id, req.Username)
	return &userpb.RegisterResp{
		UserId: id,
		Tokens: &userpb.TokenPair{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			AccessExpire:  nowUnix + int64(l.jwtMgr.ExpireSeconds()),
			RefreshExpire: nowUnix + int64(l.jwtMgr.ExpireSeconds()),
		},
		User: modelToUserInfo(user),
	}, nil
}

func modelToUserInfo(u *model.User) *userpb.UserInfo {
	return &userpb.UserInfo{
		Id:        u.ID,
		Username:  u.Username,
		Phone:     u.Phone,
		Email:     u.Email,
		Avatar:    u.Avatar,
		Gender:    u.Gender,
		Bio:       u.Bio,
		Birthday:  u.Birthday,
		Balance:   u.Balance,
		CreatedAt: u.CreatedAt.Unix(),
		UpdatedAt: u.UpdatedAt.Unix(),
	}
}
