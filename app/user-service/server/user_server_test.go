package server

import (
	"context"
	"net"
	"testing"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/logx"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type MockAuthLogic struct {
	mock.Mock
}

func (m *MockAuthLogic) Register(ctx context.Context, req *userpb.RegisterReq) (*userpb.RegisterResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.RegisterResp), args.Error(1)
}

func (m *MockAuthLogic) Login(ctx context.Context, req *userpb.LoginReq) (*userpb.LoginResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.LoginResp), args.Error(1)
}

func (m *MockAuthLogic) Logout(ctx context.Context, req *userpb.LogoutReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

func (m *MockAuthLogic) RefreshToken(ctx context.Context, req *userpb.RefreshTokenReq) (*userpb.RefreshTokenResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.RefreshTokenResp), args.Error(1)
}

func (m *MockAuthLogic) OAuthLogin(ctx context.Context, req *userpb.OAuthLoginReq) (*userpb.LoginResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.LoginResp), args.Error(1)
}

func (m *MockAuthLogic) ValidateToken(ctx context.Context, req *userpb.ValidateTokenReq) (*userpb.ValidateTokenResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.ValidateTokenResp), args.Error(1)
}

func (m *MockAuthLogic) GetSessions(ctx context.Context, userID int64) (*userpb.GetSessionsResp, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.GetSessionsResp), args.Error(1)
}

func (m *MockAuthLogic) RevokeSession(ctx context.Context, userID int64, req *userpb.RevokeSessionReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

type MockUserLogic struct {
	mock.Mock
}

func (m *MockUserLogic) GetProfile(ctx context.Context, userID int64) (*userpb.UserInfo, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.UserInfo), args.Error(1)
}

func (m *MockUserLogic) UpdateProfile(ctx context.Context, userID int64, req *userpb.UpdateProfileReq) (*userpb.UserInfo, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.UserInfo), args.Error(1)
}

func (m *MockUserLogic) UpdatePassword(ctx context.Context, userID int64, req *userpb.UpdatePasswordReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

func (m *MockUserLogic) BindPhone(ctx context.Context, userID int64, req *userpb.BindPhoneReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

func (m *MockUserLogic) BindEmail(ctx context.Context, userID int64, req *userpb.BindEmailReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

func (m *MockUserLogic) GetUserInfo(ctx context.Context, req *userpb.GetUserInfoReq) (*userpb.UserInfo, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.UserInfo), args.Error(1)
}

func (m *MockUserLogic) BatchGetUserInfo(ctx context.Context, req *userpb.BatchGetUserInfoReq) (*userpb.BatchGetUserInfoResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.BatchGetUserInfoResp), args.Error(1)
}

func (m *MockUserLogic) SearchUsers(ctx context.Context, req *userpb.SearchUsersReq) (*userpb.SearchUsersResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.SearchUsersResp), args.Error(1)
}

func (m *MockUserLogic) ListAllUserIDs(ctx context.Context) (*userpb.ListAllUserIDsResp, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.ListAllUserIDsResp), args.Error(1)
}

func (m *MockUserLogic) GetSettings(ctx context.Context, userID int64) (*userpb.GetSettingsResp, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.GetSettingsResp), args.Error(1)
}

func (m *MockUserLogic) UpdateSettings(ctx context.Context, userID int64, req *userpb.UpdateSettingsReq) (*commonpb.BaseResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*commonpb.BaseResponse), args.Error(1)
}

func (m *MockUserLogic) Recharge(ctx context.Context, req *userpb.RechargeReq) (*userpb.RechargeResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.RechargeResp), args.Error(1)
}

func (m *MockUserLogic) DeductBalance(ctx context.Context, req *userpb.DeductBalanceReq) (*userpb.DeductBalanceResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.DeductBalanceResp), args.Error(1)
}

func (m *MockUserLogic) GetBalance(ctx context.Context, req *userpb.GetBalanceReq) (*userpb.GetBalanceResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.GetBalanceResp), args.Error(1)
}

type MockStatusLogic struct {
	mock.Mock
}

func (m *MockStatusLogic) BatchGetStatus(ctx context.Context, req *userpb.BatchGetStatusReq) (*userpb.BatchGetStatusResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*userpb.BatchGetStatusResp), args.Error(1)
}

func setupTestServer(t *testing.T) (*UserServer, *MockAuthLogic, *MockUserLogic, *MockStatusLogic, userpb.UserServiceClient, func()) {
	t.Helper()

	authL := new(MockAuthLogic)
	userL := new(MockUserLogic)
	statusL := new(MockStatusLogic)

	srv := NewUserServer(&UserServerContext{
		AuthLogic:   authL,
		UserLogic:   userL,
		StatusLogic: statusL,
	}, logx.DefaultLogger())

	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	userpb.RegisterUserServiceServer(gs, srv)

	go func() {
		_ = gs.Serve(lis)
	}()

	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}

	client := userpb.NewUserServiceClient(conn)
	cleanup := func() {
		conn.Close()
		gs.Stop()
	}

	return srv, authL, userL, statusL, client, cleanup
}

func TestServer_Register(t *testing.T) {
	_, authL, _, _, client, cleanup := setupTestServer(t)
	defer cleanup()

	authL.On("Register", mock.Anything, &userpb.RegisterReq{
		Username: "newuser",
		Password: "pass123",
	}).Return(&userpb.RegisterResp{
		UserId: 12345,
		Tokens: &userpb.TokenPair{AccessToken: "access-token"},
		User:   &userpb.UserInfo{Id: 12345, Username: "newuser"},
	}, nil)

	resp, err := client.Register(context.Background(), &userpb.RegisterReq{
		Username: "newuser",
		Password: "pass123",
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(12345), resp.UserId)
	assert.Equal(t, "access-token", resp.Tokens.AccessToken)
}

func TestServer_Login(t *testing.T) {
	_, authL, _, _, client, cleanup := setupTestServer(t)
	defer cleanup()

	authL.On("Login", mock.Anything, &userpb.LoginReq{
		Account:  "testuser",
		Password: "pass123",
	}).Return(&userpb.LoginResp{
		UserId: 1,
		Tokens: &userpb.TokenPair{AccessToken: "access-token"},
	}, nil)

	resp, err := client.Login(context.Background(), &userpb.LoginReq{
		Account:  "testuser",
		Password: "pass123",
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.UserId)
}

func TestServer_GetUserInfo(t *testing.T) {
	_, _, userL, _, client, cleanup := setupTestServer(t)
	defer cleanup()

	userL.On("GetUserInfo", mock.Anything, &userpb.GetUserInfoReq{UserId: 1}).Return(&userpb.UserInfo{
		Id:       1,
		Username: "targetuser",
	}, nil)

	resp, err := client.GetUserInfo(context.Background(), &userpb.GetUserInfoReq{UserId: 1})
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Id)
	assert.Equal(t, "targetuser", resp.Username)
}

func TestServer_BatchGetUserInfo(t *testing.T) {
	_, _, userL, _, client, cleanup := setupTestServer(t)
	defer cleanup()

	userL.On("BatchGetUserInfo", mock.Anything, &userpb.BatchGetUserInfoReq{
		UserIds: []int64{1, 2},
	}).Return(&userpb.BatchGetUserInfoResp{
		Users: []*userpb.UserInfo{
			{Id: 1, Username: "user1"},
			{Id: 2, Username: "user2"},
		},
	}, nil)

	resp, err := client.BatchGetUserInfo(context.Background(), &userpb.BatchGetUserInfoReq{
		UserIds: []int64{1, 2},
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Users, 2)
}

func TestServer_SearchUsers(t *testing.T) {
	_, _, userL, _, client, cleanup := setupTestServer(t)
	defer cleanup()

	userL.On("SearchUsers", mock.Anything, &userpb.SearchUsersReq{Keyword: "test"}).Return(&userpb.SearchUsersResp{
		Users: []*userpb.UserInfo{
			{Id: 1, Username: "test1"},
		},
		Pagination: &commonpb.PaginationResp{Total: 1, Page: 1},
	}, nil)

	resp, err := client.SearchUsers(context.Background(), &userpb.SearchUsersReq{Keyword: "test"})
	assert.NoError(t, err)
	assert.Len(t, resp.Users, 1)
}

func TestServer_BatchGetStatus(t *testing.T) {
	_, _, _, statusL, client, cleanup := setupTestServer(t)
	defer cleanup()

	statusL.On("BatchGetStatus", mock.Anything, &userpb.BatchGetStatusReq{
		UserIds: []int64{1},
	}).Return(&userpb.BatchGetStatusResp{
		Statuses: []*userpb.UserStatus{
			{UserId: 1, IsOnline: true},
		},
	}, nil)

	resp, err := client.BatchGetStatus(context.Background(), &userpb.BatchGetStatusReq{
		UserIds: []int64{1},
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Statuses, 1)
	assert.True(t, resp.Statuses[0].IsOnline)
}

func TestServer_ContextUserID(t *testing.T) {
	ctx := ContextWithUserID(context.Background(), 42)
	assert.Equal(t, int64(42), UserIDFromContext(ctx))
}

func TestServer_ContextUserID_Empty(t *testing.T) {
	assert.Equal(t, int64(0), UserIDFromContext(context.Background()))
}

func TestNewUserServer(t *testing.T) {
	srv := NewUserServer(&UserServerContext{}, logx.DefaultLogger())
	assert.NotNil(t, srv)
}
