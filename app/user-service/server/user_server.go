package server

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/logx"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
)

func ContextWithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, interceptor.ContextKeyUserID, userID)
}

func UserIDFromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(interceptor.ContextKeyUserID).(int64)
	return v
}

type UserServer struct {
	userpb.UnimplementedUserServiceServer
	ctx    *UserServerContext
	logger logx.Logger
}

type UserServerContext struct {
	AuthLogic   AuthLogic
	UserLogic   UserLogic
	StatusLogic StatusLogic
}

type AuthLogic interface {
	Register(ctx context.Context, req *userpb.RegisterReq) (*userpb.RegisterResp, error)
	Login(ctx context.Context, req *userpb.LoginReq) (*userpb.LoginResp, error)
	Logout(ctx context.Context, req *userpb.LogoutReq) (*commonpb.BaseResponse, error)
	RefreshToken(ctx context.Context, req *userpb.RefreshTokenReq) (*userpb.RefreshTokenResp, error)
	OAuthLogin(ctx context.Context, req *userpb.OAuthLoginReq) (*userpb.LoginResp, error)
	ValidateToken(ctx context.Context, req *userpb.ValidateTokenReq) (*userpb.ValidateTokenResp, error)
	GetSessions(ctx context.Context, userID int64) (*userpb.GetSessionsResp, error)
	RevokeSession(ctx context.Context, userID int64, req *userpb.RevokeSessionReq) (*commonpb.BaseResponse, error)
}

type UserLogic interface {
	GetProfile(ctx context.Context, userID int64) (*userpb.UserInfo, error)
	UpdateProfile(ctx context.Context, userID int64, req *userpb.UpdateProfileReq) (*userpb.UserInfo, error)
	UpdatePassword(ctx context.Context, userID int64, req *userpb.UpdatePasswordReq) (*commonpb.BaseResponse, error)
	BindPhone(ctx context.Context, userID int64, req *userpb.BindPhoneReq) (*commonpb.BaseResponse, error)
	BindEmail(ctx context.Context, userID int64, req *userpb.BindEmailReq) (*commonpb.BaseResponse, error)
	GetUserInfo(ctx context.Context, req *userpb.GetUserInfoReq) (*userpb.UserInfo, error)
	BatchGetUserInfo(ctx context.Context, req *userpb.BatchGetUserInfoReq) (*userpb.BatchGetUserInfoResp, error)
	SearchUsers(ctx context.Context, req *userpb.SearchUsersReq) (*userpb.SearchUsersResp, error)
	ListAllUserIDs(ctx context.Context) (*userpb.ListAllUserIDsResp, error)
	GetSettings(ctx context.Context, userID int64) (*userpb.GetSettingsResp, error)
	UpdateSettings(ctx context.Context, userID int64, req *userpb.UpdateSettingsReq) (*commonpb.BaseResponse, error)
	Recharge(ctx context.Context, req *userpb.RechargeReq) (*userpb.RechargeResp, error)
	DeductBalance(ctx context.Context, req *userpb.DeductBalanceReq) (*userpb.DeductBalanceResp, error)
	GetBalance(ctx context.Context, req *userpb.GetBalanceReq) (*userpb.GetBalanceResp, error)
}

type StatusLogic interface {
	BatchGetStatus(ctx context.Context, req *userpb.BatchGetStatusReq) (*userpb.BatchGetStatusResp, error)
}

func NewUserServer(ctx *UserServerContext, logger logx.Logger) *UserServer {
	return &UserServer{ctx: ctx, logger: logger}
}

func (s *UserServer) Register(ctx context.Context, req *userpb.RegisterReq) (*userpb.RegisterResp, error) {
	resp, err := s.ctx.AuthLogic.Register(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=Register error=%v", err)
		return nil, err
	}
	logger.Infof("method=Register user_id=%d", resp.UserId)
	return resp, nil
}

func (s *UserServer) Login(ctx context.Context, req *userpb.LoginReq) (*userpb.LoginResp, error) {
	resp, err := s.ctx.AuthLogic.Login(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=Login error=%v", err)
		return nil, err
	}
	logger.Infof("method=Login user_id=%d", resp.UserId)
	return resp, nil
}

func (s *UserServer) Logout(ctx context.Context, req *userpb.LogoutReq) (*commonpb.BaseResponse, error) {
	resp, err := s.ctx.AuthLogic.Logout(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=Logout error=%v", err)
		return nil, err
	}
	logger.Infof("method=Logout")
	return resp, nil
}

func (s *UserServer) RefreshToken(ctx context.Context, req *userpb.RefreshTokenReq) (*userpb.RefreshTokenResp, error) {
	resp, err := s.ctx.AuthLogic.RefreshToken(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=RefreshToken error=%v", err)
		return nil, err
	}
	logger.Infof("method=RefreshToken")
	return resp, nil
}

func (s *UserServer) OAuthLogin(ctx context.Context, req *userpb.OAuthLoginReq) (*userpb.LoginResp, error) {
	resp, err := s.ctx.AuthLogic.OAuthLogin(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=OAuthLogin error=%v", err)
		return nil, err
	}
	logger.Infof("method=OAuthLogin user_id=%d", resp.UserId)
	return resp, nil
}

func (s *UserServer) ValidateToken(ctx context.Context, req *userpb.ValidateTokenReq) (*userpb.ValidateTokenResp, error) {
	resp, err := s.ctx.AuthLogic.ValidateToken(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=ValidateToken error=%v", err)
		return nil, err
	}
	logger.Infof("method=ValidateToken user_id=%d", resp.UserId)
	return resp, nil
}

func (s *UserServer) GetSessions(ctx context.Context, _ *commonpb.Empty) (*userpb.GetSessionsResp, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.AuthLogic.GetSessions(ctx, userID)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=GetSessions user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=GetSessions user_id=%d sessions=%d", userID, len(resp.Sessions))
	return resp, nil
}

func (s *UserServer) RevokeSession(ctx context.Context, req *userpb.RevokeSessionReq) (*commonpb.BaseResponse, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.AuthLogic.RevokeSession(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=RevokeSession user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=RevokeSession user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) GetProfile(ctx context.Context, _ *commonpb.Empty) (*userpb.UserInfo, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.GetProfile(ctx, userID)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=GetProfile user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=GetProfile user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) UpdateProfile(ctx context.Context, req *userpb.UpdateProfileReq) (*userpb.UserInfo, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.UpdateProfile(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=UpdateProfile user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=UpdateProfile user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) UpdatePassword(ctx context.Context, req *userpb.UpdatePasswordReq) (*commonpb.BaseResponse, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.UpdatePassword(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=UpdatePassword user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=UpdatePassword user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) BindPhone(ctx context.Context, req *userpb.BindPhoneReq) (*commonpb.BaseResponse, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.BindPhone(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=BindPhone user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=BindPhone user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) BindEmail(ctx context.Context, req *userpb.BindEmailReq) (*commonpb.BaseResponse, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.BindEmail(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=BindEmail user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=BindEmail user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) GetUserInfo(ctx context.Context, req *userpb.GetUserInfoReq) (*userpb.UserInfo, error) {
	resp, err := s.ctx.UserLogic.GetUserInfo(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=GetUserInfo req_user_id=%d error=%v", req.UserId, err)
		return nil, err
	}
	logger.Infof("method=GetUserInfo user_id=%d", resp.Id)
	return resp, nil
}

func (s *UserServer) BatchGetUserInfo(ctx context.Context, req *userpb.BatchGetUserInfoReq) (*userpb.BatchGetUserInfoResp, error) {
	resp, err := s.ctx.UserLogic.BatchGetUserInfo(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=BatchGetUserInfo error=%v", err)
		return nil, err
	}
	logger.Infof("method=BatchGetUserInfo count=%d", len(resp.Users))
	return resp, nil
}

func (s *UserServer) SearchUsers(ctx context.Context, req *userpb.SearchUsersReq) (*userpb.SearchUsersResp, error) {
	resp, err := s.ctx.UserLogic.SearchUsers(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=SearchUsers error=%v", err)
		return nil, err
	}
	logger.Infof("method=SearchUsers count=%d", len(resp.Users))
	return resp, nil
}

func (s *UserServer) ListAllUserIDs(ctx context.Context, _ *commonpb.Empty) (*userpb.ListAllUserIDsResp, error) {
	resp, err := s.ctx.UserLogic.ListAllUserIDs(ctx)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=ListAllUserIDs error=%v", err)
		return nil, err
	}
	logger.Infof("method=ListAllUserIDs count=%d", len(resp.UserIds))
	return resp, nil
}

func (s *UserServer) BatchGetStatus(ctx context.Context, req *userpb.BatchGetStatusReq) (*userpb.BatchGetStatusResp, error) {
	resp, err := s.ctx.StatusLogic.BatchGetStatus(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=BatchGetStatus error=%v", err)
		return nil, err
	}
	logger.Infof("method=BatchGetStatus count=%d", len(resp.Statuses))
	return resp, nil
}

func (s *UserServer) GetSettings(ctx context.Context, _ *commonpb.Empty) (*userpb.GetSettingsResp, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.GetSettings(ctx, userID)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=GetSettings user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=GetSettings user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) UpdateSettings(ctx context.Context, req *userpb.UpdateSettingsReq) (*commonpb.BaseResponse, error) {
	userID := UserIDFromContext(ctx)
	resp, err := s.ctx.UserLogic.UpdateSettings(ctx, userID, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=UpdateSettings user_id=%d error=%v", userID, err)
		return nil, err
	}
	logger.Infof("method=UpdateSettings user_id=%d", userID)
	return resp, nil
}

func (s *UserServer) Recharge(ctx context.Context, req *userpb.RechargeReq) (*userpb.RechargeResp, error) {
	resp, err := s.ctx.UserLogic.Recharge(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=Recharge user_id=%d error=%v", req.UserId, err)
		return nil, err
	}
	logger.Infof("method=Recharge user_id=%d new_balance=%f", req.UserId, resp.NewBalance)
	return resp, nil
}

func (s *UserServer) DeductBalance(ctx context.Context, req *userpb.DeductBalanceReq) (*userpb.DeductBalanceResp, error) {
	resp, err := s.ctx.UserLogic.DeductBalance(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=DeductBalance user_id=%d error=%v", req.UserId, err)
		return nil, err
	}
	logger.Infof("method=DeductBalance user_id=%d new_balance=%f", req.UserId, resp.NewBalance)
	return resp, nil
}

func (s *UserServer) GetBalance(ctx context.Context, req *userpb.GetBalanceReq) (*userpb.GetBalanceResp, error) {
	resp, err := s.ctx.UserLogic.GetBalance(ctx, req)
	logger := s.logger.WithContext(ctx)
	if err != nil {
		logger.Errorf("method=GetBalance user_id=%d error=%v", req.UserId, err)
		return nil, err
	}
	logger.Infof("method=GetBalance user_id=%d balance=%f", req.UserId, resp.Balance)
	return resp, nil
}
