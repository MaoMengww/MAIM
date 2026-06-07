package auth

import (
	"fmt"
	"context"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/metrics"
	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserRepo defines user repository methods needed by this package.
type UserRepo interface {
	Create(ctx context.Context, user *model.User) error
	GetByID(ctx context.Context, id int64) (*model.User, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	GetByPhone(ctx context.Context, phone string) (*model.User, error)
	GetByEmail(ctx context.Context, email string) (*model.User, error)
}

// AuthRepo defines auth repository methods needed by this package.
type AuthRepo interface {
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	SaveDevice(ctx context.Context, d *model.UserDevice) error
	GetUserDevices(ctx context.Context, userID int64) ([]*model.UserDevice, error)
	DeleteDevice(ctx context.Context, userID int64, deviceID string) error
	IsDeviceOnline(ctx context.Context, userID int64, deviceID string) (bool, error)
}

type Logic struct {
	userRepo UserRepo
	authRepo AuthRepo
	snow     *snowflake.Node
	jwtMgr   *jwt.Manager
	logger   logx.Logger
}

func New(userRepo UserRepo, authRepo AuthRepo, snow *snowflake.Node, jwtMgr *jwt.Manager, logger logx.Logger) *Logic {
	return &Logic{userRepo: userRepo, authRepo: authRepo, snow: snow, jwtMgr: jwtMgr, logger: logger}
}

func (l *Logic) ctxLogger(ctx context.Context) logx.Logger {
	if l.logger != nil {
		return l.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
}

func (l *Logic) Register(ctx context.Context, req *userpb.RegisterReq) (*userpb.RegisterResp, error) {
	logger := l.ctxLogger(ctx)

	if req.Username == "" || req.Password == "" {
		err := errors.New(1001, "username and password are required")
		logger.Errorf("method=Register error=%v", err)
		return nil, err
	}

	exist, err := l.userRepo.GetByUsername(ctx, req.Username)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(1010, "check username failed", err)
	}
	if exist != nil {
		err := errors.New(1005, "username already exists")
		logger.Errorf("method=Register error=%v", err)
		return nil, err
	}

	if req.Phone != "" {
		exist, _ = l.userRepo.GetByPhone(ctx, req.Phone)
		if exist != nil {
			err := errors.New(1005, "phone already registered")
			logger.Errorf("method=Register error=%v", err)
			return nil, err
		}
	}
	if req.Email != "" {
		exist, _ = l.userRepo.GetByEmail(ctx, req.Email)
		if exist != nil {
			err := errors.New(1005, "email already registered")
			logger.Errorf("method=Register error=%v", err)
			return nil, err
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Wrap(1006, "hash password failed", err)
	}

	id, err := l.snow.Generate()
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
		return nil, errors.Wrap(1010, "create user failed", err)
	}

	source := "username"
	if req.Email != "" {
		source = "email"
	} else if req.Phone != "" {
		source = "phone"
	}
	metrics.UserRegistrationsTotal.Inc(source)

	userIDStr := strconv.FormatInt(id, 10)
	accessToken, err := l.jwtMgr.Generate(userIDStr, req.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(userIDStr, req.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate refresh token failed", err)
	}

	if req.DeviceId != "" {
		device := &model.UserDevice{
			UserID:   id,
			DeviceID: req.DeviceId,
			Platform: req.Platform,
		}
		_ = l.authRepo.SaveDevice(ctx, device)
	}

	nowUnix := time.Now().Unix()
	logger.Infof("method=Register user_id=%d username=%s", id, req.Username)
	return &userpb.RegisterResp{
		UserId: id,
		Tokens: &userpb.TokenPair{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			AccessExpire:  nowUnix + 3600,
			RefreshExpire: nowUnix + 2592000,
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
