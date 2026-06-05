package auth

import (
	"context"
	"strconv"
	"time"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
)

func (l *Logic) Logout(ctx context.Context, req *userpb.LogoutReq) (*commonpb.BaseResponse, error) {
	if req.TokenId == "" {
		return nil, errors.New(1001, "token_id is required")
	}

	claims, err := l.jwtMgr.ParseIgnoreExpiry(req.TokenId)
	if err != nil {
		// token is already invalid, nothing to revoke
		return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
	}

	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl > 0 {
		if err := l.authRepo.RevokeToken(ctx, claims.ID, ttl); err != nil {
			return nil, errors.Wrap(1011, "revoke token failed", err)
		}
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}

func (l *Logic) RefreshToken(ctx context.Context, req *userpb.RefreshTokenReq) (*userpb.RefreshTokenResp, error) {
	if req.RefreshToken == "" {
		return nil, errors.New(1001, "refresh_token is required")
	}

	claims, err := l.jwtMgr.Parse(req.RefreshToken)
	if err != nil {
		return nil, errors.Wrap(1002, "invalid or expired refresh token", err)
	}

	revoked, err := l.authRepo.IsTokenRevoked(ctx, claims.ID)
	if err != nil {
		return nil, errors.Wrap(1011, "check token revoked failed", err)
	}
	if revoked {
		return nil, errors.New(1002, "refresh token has been revoked")
	}

	return l.buildTokenResp(ctx, claims.UserID)
}

func (l *Logic) ValidateToken(ctx context.Context, req *userpb.ValidateTokenReq) (*userpb.ValidateTokenResp, error) {
	if req.AccessToken == "" {
		return &userpb.ValidateTokenResp{Valid: false}, nil
	}
	claims, err := l.jwtMgr.Parse(req.AccessToken)
	if err != nil {
		return &userpb.ValidateTokenResp{Valid: false}, nil
	}
	userID, _ := strconv.ParseInt(claims.UserID, 10, 64)
	return &userpb.ValidateTokenResp{
		Valid:     true,
		UserId:    userID,
		DeviceId:  "",
		ExpiresAt: claims.ExpiresAt.Unix(),
	}, nil
}

func (l *Logic) buildTokenResp(ctx context.Context, userIDStr string) (*userpb.RefreshTokenResp, error) {
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return nil, errors.New(1001, "invalid user_id")
	}

	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(1010, "query user failed", err)
	}
	if u == nil {
		return nil, errors.New(1004, "user not found")
	}

	accessToken, err := l.jwtMgr.Generate(userIDStr, u.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate access token failed", err)
	}
	refreshToken, err := l.jwtMgr.Generate(userIDStr, u.Username)
	if err != nil {
		return nil, errors.Wrap(1002, "generate refresh token failed", err)
	}

	nowUnix := time.Now().Unix()
	return &userpb.RefreshTokenResp{
		Tokens: &userpb.TokenPair{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			AccessExpire:  nowUnix + consts.JWTDefaultExpireSec,
			RefreshExpire: nowUnix + consts.JWTDefaultRefreshSec,
		},
		User: modelToUserInfo(u),
	}, nil
}
