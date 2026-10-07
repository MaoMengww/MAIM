package user

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/logx"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserRepo defines the user repository methods needed by this package.
type UserRepo interface {
	GetByID(ctx context.Context, id string) (*model.User, error)
	GetByPhone(ctx context.Context, phone string) (*model.User, error)
	GetByEmail(ctx context.Context, email string) (*model.User, error)
	Update(ctx context.Context, id string, updates map[string]any) error
	BatchGetByIDs(ctx context.Context, ids []string) ([]*model.User, error)
	Search(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error)
	ListAllIDs(ctx context.Context) ([]string, error)
	UpdateBalance(ctx context.Context, userID string, delta float64) (float64, error)
	GetBalance(ctx context.Context, userID string) (float64, error)
}

type Logic struct {
	userRepo UserRepo
	logger   logx.Logger
}

func New(userRepo UserRepo, logger logx.Logger) *Logic {
	return &Logic{userRepo: userRepo, logger: logger}
}

func (l *Logic) ctxLogger(ctx context.Context) logx.Logger {
	if l.logger != nil {
		return l.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
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

func (l *Logic) GetProfile(ctx context.Context, userID string) (*userpb.UserInfo, error) {
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	u, err := l.userRepo.GetByID(ctx, userID)
	logger := l.ctxLogger(ctx)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Errorf("method=GetProfile user_id=%s error=user not found", userID)
			return nil, errors.New(errors.CodeNotFound, "user not found")
		}
		logger.Errorf("method=GetProfile user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	logger.Infof("method=GetProfile user_id=%s username=%s", userID, u.Username)
	return modelToUserInfo(u), nil
}

func (l *Logic) UpdateProfile(ctx context.Context, userID string, req *userpb.UpdateProfileReq) (*userpb.UserInfo, error) {
	if req.UserId != "" && req.UserId != userID {
		return nil, errors.New(errors.CodeForbidden, "account is not owned by caller")
	}
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	u, err := l.userRepo.GetByID(ctx, userID)
	logger := l.ctxLogger(ctx)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Errorf("method=UpdateProfile user_id=%s error=user not found", userID)
			return nil, errors.New(errors.CodeNotFound, "user not found")
		}
		logger.Errorf("method=UpdateProfile user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	if req.Avatar != nil {
		u.Avatar = *req.Avatar
	}
	if req.Gender != nil {
		u.Gender = *req.Gender
	}
	if req.Bio != nil {
		u.Bio = *req.Bio
	}
	if req.Birthday != nil {
		u.Birthday = *req.Birthday
	}
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"avatar":   u.Avatar,
		"gender":   u.Gender,
		"bio":      u.Bio,
		"birthday": u.Birthday,
	}); err != nil {
		logger.Errorf("method=UpdateProfile user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "update user failed", err)
	}
	logger.Infof("method=UpdateProfile user_id=%s", userID)
	return modelToUserInfo(u), nil
}

func (l *Logic) UpdatePassword(ctx context.Context, userID string, req *userpb.UpdatePasswordReq) (*commonpb.BaseResponse, error) {
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	logger := l.ctxLogger(ctx)

	if req.OldPassword == "" || req.NewPassword == "" {
		err := errors.New(errors.CodeInvalidParam, "old_password and new_password are required")
		logger.Errorf("method=UpdatePassword user_id=%s error=%v", userID, err)
		return nil, err
	}
	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Errorf("method=UpdatePassword user_id=%s error=user not found", userID)
			return nil, errors.New(errors.CodeNotFound, "user not found")
		}
		logger.Errorf("method=UpdatePassword user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)); err != nil {
		logger.Errorf("method=UpdatePassword user_id=%s error=old password is incorrect", userID)
		return nil, errors.New(errors.CodeUnauthorized, "old password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "hash password failed", err)
	}
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"password_hash": string(hash),
	}); err != nil {
		logger.Errorf("method=UpdatePassword user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "update password failed", err)
	}
	logger.Infof("method=UpdatePassword user_id=%s", userID)
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}

func (l *Logic) BindPhone(ctx context.Context, userID string, req *userpb.BindPhoneReq) (*commonpb.BaseResponse, error) {
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	logger := l.ctxLogger(ctx)

	if req.Phone == "" {
		err := errors.New(errors.CodeInvalidParam, "phone is required")
		logger.Errorf("method=BindPhone user_id=%s error=%v", userID, err)
		return nil, err
	}
	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		logger.Errorf("method=BindPhone user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	exist, _ := l.userRepo.GetByPhone(ctx, req.Phone)
	if exist != nil && exist.ID != userID {
		err := errors.New(errors.CodeConflict, "phone already bound")
		logger.Errorf("method=BindPhone user_id=%s error=%v", userID, err)
		return nil, err
	}
	u.Phone = req.Phone
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"phone": req.Phone,
	}); err != nil {
		logger.Errorf("method=BindPhone user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "bind phone failed", err)
	}
	logger.Infof("method=BindPhone user_id=%s", userID)
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}

func (l *Logic) BindEmail(ctx context.Context, userID string, req *userpb.BindEmailReq) (*commonpb.BaseResponse, error) {
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	logger := l.ctxLogger(ctx)

	if req.Email == "" {
		err := errors.New(errors.CodeInvalidParam, "email is required")
		logger.Errorf("method=BindEmail user_id=%s error=%v", userID, err)
		return nil, err
	}
	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		logger.Errorf("method=BindEmail user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	exist, _ := l.userRepo.GetByEmail(ctx, req.Email)
	if exist != nil && exist.ID != userID {
		err := errors.New(errors.CodeConflict, "email already bound")
		logger.Errorf("method=BindEmail user_id=%s error=%v", userID, err)
		return nil, err
	}
	u.Email = req.Email
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"email": req.Email,
	}); err != nil {
		logger.Errorf("method=BindEmail user_id=%s error=%v", userID, err)
		return nil, errors.Wrap(errors.CodeDBError, "bind email failed", err)
	}
	logger.Infof("method=BindEmail user_id=%s", userID)
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}
func authorizeAccount(ctx context.Context, userID string) error {
	if identity.Validate(userID) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid user_id")
	}
	owner, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(owner) != nil {
		return errors.New(errors.CodeUnauthorized, "authentication required")
	}
	if owner != userID {
		return errors.New(errors.CodeForbidden, "account is not owned by caller")
	}
	return nil
}
