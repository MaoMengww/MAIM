package auth

import (
	"context"
	"time"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/jwt"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"gorm.io/gorm"
)

func (l *Logic) activeSession(ctx context.Context, claims *jwt.Claims) (bool, error) {
	revoked, err := l.authRepo.IsTokenRevoked(ctx, claims.ID)
	if err != nil {
		return false, errors.Wrap(errors.CodeCacheError, "check token revoked failed", err)
	}
	if revoked {
		return false, nil
	}
	device, err := l.authRepo.GetDevice(ctx, claims.UserID, claims.DeviceID)
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(errors.CodeDBError, "get device failed", err)
	}
	return device != nil && device.ID == claims.SessionID, nil
}

func (l *Logic) Logout(ctx context.Context, req *userpb.LogoutReq) (*commonpb.BaseResponse, error) {
	claims, err := l.jwtMgr.ParseIgnoreExpiry(req.TokenId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "invalid logout token", err)
	}
	owner, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if owner == "" {
		return nil, errors.New(errors.CodeUnauthorized, "authentication required")
	}
	if owner != claims.UserID {
		return nil, errors.New(errors.CodeForbidden, "token belongs to another account")
	}
	device, err := l.authRepo.GetDevice(ctx, claims.UserID, claims.DeviceID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, errors.Wrap(errors.CodeDBError, "get device failed", err)
	}
	if device != nil && device.ID == claims.SessionID {
		if err := l.authRepo.DeleteSession(ctx, claims.UserID, claims.SessionID); err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "delete device failed", err)
		}
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		if err := l.authRepo.RevokeToken(ctx, claims.ID, ttl); err != nil {
			return nil, errors.Wrap(errors.CodeCacheError, "revoke token failed", err)
		}
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}

func (l *Logic) RefreshToken(ctx context.Context, req *userpb.RefreshTokenReq) (*userpb.RefreshTokenResp, error) {
	claims, err := l.jwtMgr.Parse(req.RefreshToken)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "invalid or expired refresh token", err)
	}
	active, err := l.activeSession(ctx, claims)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, errors.New(errors.CodeUnauthorized, "session has been revoked")
	}
	return l.buildTokenResp(ctx, claims)
}

func (l *Logic) ValidateToken(ctx context.Context, req *userpb.ValidateTokenReq) (*userpb.ValidateTokenResp, error) {
	claims, err := l.jwtMgr.Parse(req.AccessToken)
	if err != nil {
		return &userpb.ValidateTokenResp{Valid: false}, nil
	}
	active, err := l.activeSession(ctx, claims)
	if err != nil {
		return nil, err
	}
	if !active {
		return &userpb.ValidateTokenResp{Valid: false}, nil
	}
	return &userpb.ValidateTokenResp{Valid: true, UserId: &claims.UserID, DeviceId: claims.DeviceID, ExpiresAt: claims.ExpiresAt.Unix()}, nil
}

func (l *Logic) buildTokenResp(ctx context.Context, claims *jwt.Claims) (*userpb.RefreshTokenResp, error) {
	u, err := l.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "query user failed", err)
	}
	if u == nil {
		return nil, errors.New(errors.CodeNotFound, "user not found")
	}
	accessToken, err := l.jwtMgr.Generate(u.ID, u.Username, claims.DeviceID, claims.SessionID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(u.ID, u.Username, claims.DeviceID, claims.SessionID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "generate refresh token failed", err)
	}
	nowUnix := time.Now().Unix()
	return &userpb.RefreshTokenResp{Tokens: &userpb.TokenPair{AccessToken: accessToken, RefreshToken: refreshToken, AccessExpire: nowUnix + int64(l.jwtMgr.ExpireSeconds()), RefreshExpire: nowUnix + int64(l.jwtMgr.ExpireSeconds())}, User: modelToUserInfo(u)}, nil
}
