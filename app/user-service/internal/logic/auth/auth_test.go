package auth

import (
	"context"
	"testing"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

type MockUserRepo struct {
	mock.Mock
}

func (m *MockUserRepo) Create(ctx context.Context, user *model.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepo) GetByID(ctx context.Context, id int64) (*model.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.User), args.Error(1)
}

func (m *MockUserRepo) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.User), args.Error(1)
}

func (m *MockUserRepo) GetByPhone(ctx context.Context, phone string) (*model.User, error) {
	args := m.Called(ctx, phone)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.User), args.Error(1)
}

func (m *MockUserRepo) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.User), args.Error(1)
}

func (m *MockUserRepo) Update(ctx context.Context, user *model.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepo) UpdatePassword(ctx context.Context, id int64, hash string) error {
	args := m.Called(ctx, id, hash)
	return args.Error(0)
}

func (m *MockUserRepo) Search(ctx context.Context, keyword string, offset, limit int) ([]*model.User, int64, error) {
	args := m.Called(ctx, keyword, offset, limit)
	return args.Get(0).([]*model.User), args.Get(1).(int64), args.Error(2)
}

func (m *MockUserRepo) BatchGetByIDs(ctx context.Context, ids []int64) ([]*model.User, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*model.User), args.Error(1)
}

func (m *MockUserRepo) ListAllIDs(ctx context.Context) ([]int64, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]int64), args.Error(1)
}

type MockAuthRepo struct {
	mock.Mock
}

func (m *MockAuthRepo) SaveDevice(ctx context.Context, d *model.UserDevice) error {
	args := m.Called(ctx, d)
	return args.Error(0)
}

func (m *MockAuthRepo) GetUserDevices(ctx context.Context, userID int64) ([]*model.UserDevice, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*model.UserDevice), args.Error(1)
}

func (m *MockAuthRepo) DeleteDevice(ctx context.Context, userID int64, deviceID string) error {
	args := m.Called(ctx, userID, deviceID)
	return args.Error(0)
}

func (m *MockAuthRepo) GetDevice(ctx context.Context, userID int64, deviceID string) (*model.UserDevice, error) {
	args := m.Called(ctx, userID, deviceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.UserDevice), args.Error(1)
}

func (m *MockAuthRepo) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	args := m.Called(ctx, jti, ttl)
	return args.Error(0)
}

func (m *MockAuthRepo) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	args := m.Called(ctx, jti)
	return args.Bool(0), args.Error(1)
}

func (m *MockAuthRepo) IsDeviceOnline(ctx context.Context, userID int64, deviceID string) (bool, error) {
	args := m.Called(ctx, userID, deviceID)
	return args.Bool(0), args.Error(1)
}

func newTestLogic(mockUser *MockUserRepo, mockAuth *MockAuthRepo) *Logic {
	sn, _ := snowflake.NewNode(1)
	return &Logic{
		userRepo: mockUser,
		authRepo: mockAuth,
		snow:     sn,
		jwtMgr:   jwt.NewManager("test-secret", 7200, 604800),
	}
}

func TestRegister_MissingUsername(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestRegister_MissingPassword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "testuser",
		Password: "",
	})
	assert.Error(t, err)
}

func TestRegister_UsernameExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(&model.User{ID: 1}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "testuser",
		Password: "password123",
	})
	assert.Error(t, err)
	mockUser.AssertExpectations(t)
}

func TestRegister_PhoneExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByPhone", mock.Anything, "13800138000").Return(&model.User{ID: 2}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "testuser",
		Password: "password123",
		Phone:    "13800138000",
	})
	assert.Error(t, err)
}

func TestRegister_EmailExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByPhone", mock.Anything, "").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByEmail", mock.Anything, "test@example.com").Return(&model.User{ID: 3}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "testuser",
		Password: "password123",
		Email:    "test@example.com",
	})
	assert.Error(t, err)
}

func TestRegister_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockAuth := new(MockAuthRepo)

	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("Create", mock.Anything, mock.AnythingOfType("*model.User")).Return(nil)
	mockAuth.On("SaveDevice", mock.Anything, mock.Anything).Return(nil)

	l := newTestLogic(mockUser, mockAuth)
	resp, err := l.Register(context.Background(), &userpb.RegisterReq{
		Username: "testuser",
		Password: "password123",
	})
	assert.NoError(t, err)
	assert.NotZero(t, resp.UserId)
	assert.NotNil(t, resp.Tokens)
	assert.NotNil(t, resp.User)
	assert.Equal(t, "testuser", resp.User.Username)
	mockUser.AssertExpectations(t)
}

func TestLogin_MissingAccount(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Login(context.Background(), &userpb.LoginReq{
		Account:  "",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestLogin_MissingPassword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Login(context.Background(), &userpb.LoginReq{
		Account:  "testuser",
		Password: "",
	})
	assert.Error(t, err)
}

func TestLogin_UserNotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "unknown").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByPhone", mock.Anything, "unknown").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByEmail", mock.Anything, "unknown").Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Login(context.Background(), &userpb.LoginReq{
		Account:  "unknown",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestLogout_Success(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	mockAuth.On("RevokeToken", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	token, err := l.jwtMgr.Generate("1", "testuser")
	assert.NoError(t, err)

	resp, err := l.Logout(context.Background(), &userpb.LogoutReq{TokenId: token})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
	mockAuth.AssertExpectations(t)
}

func TestLogout_MissingTokenID(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Logout(context.Background(), &userpb.LogoutReq{TokenId: ""})
	assert.Error(t, err)
}

func TestValidateToken_EmptyToken(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	resp, err := l.ValidateToken(context.Background(), &userpb.ValidateTokenReq{AccessToken: ""})
	assert.NoError(t, err)
	assert.False(t, resp.Valid)
}

func TestValidateToken_Success(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	token, err := l.jwtMgr.Generate("12345", "testuser")
	assert.NoError(t, err)
	resp, err := l.ValidateToken(context.Background(), &userpb.ValidateTokenReq{AccessToken: token})
	assert.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, int64(12345), resp.UserId)
}

func TestOAuthLogin_MissingProvider(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.OAuthLogin(context.Background(), &userpb.OAuthLoginReq{
		Provider: "",
		Code:     "code123",
	})
	assert.Error(t, err)
}

func TestOAuthLogin_MissingCode(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.OAuthLogin(context.Background(), &userpb.OAuthLoginReq{
		Provider: "wechat",
		Code:     "",
	})
	assert.Error(t, err)
}

func TestOAuthLogin_NewUser(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockAuth := new(MockAuthRepo)

	oauthUsername := "wechat_code123"
	mockUser.On("GetByUsername", mock.Anything, oauthUsername).Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("Create", mock.Anything, mock.AnythingOfType("*model.User")).Return(nil)
	mockAuth.On("SaveDevice", mock.Anything, mock.Anything).Return(nil)

	l := newTestLogic(mockUser, mockAuth)
	resp, err := l.OAuthLogin(context.Background(), &userpb.OAuthLoginReq{
		Provider: "wechat",
		Code:     "code123",
	})
	assert.NoError(t, err)
	assert.NotZero(t, resp.UserId)
	assert.NotNil(t, resp.Tokens)
}

func TestGetSessions_Success(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	devices := []*model.UserDevice{
		{DeviceID: "dev1", Platform: "ios"},
		{DeviceID: "dev2", Platform: "android"},
	}
	mockAuth.On("GetUserDevices", mock.Anything, int64(1)).Return(devices, nil)
	mockAuth.On("IsDeviceOnline", mock.Anything, int64(1), "dev1").Return(true, nil)
	mockAuth.On("IsDeviceOnline", mock.Anything, int64(1), "dev2").Return(false, nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	resp, err := l.GetSessions(context.Background(), 1)
	assert.NoError(t, err)
	assert.Len(t, resp.Sessions, 2)
	assert.Equal(t, "dev1", resp.Sessions[0].SessionId)
	assert.True(t, resp.Sessions[0].IsOnline)
	assert.False(t, resp.Sessions[1].IsOnline)
}

func TestGetSessions_Empty(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	mockAuth.On("GetUserDevices", mock.Anything, int64(1)).Return([]*model.UserDevice{}, nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	resp, err := l.GetSessions(context.Background(), 1)
	assert.NoError(t, err)
	assert.Len(t, resp.Sessions, 0)
}

func TestRevokeSession_Success(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	mockAuth.On("DeleteDevice", mock.Anything, int64(1), "dev1").Return(nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	resp, err := l.RevokeSession(context.Background(), 1, &userpb.RevokeSessionReq{SessionId: "dev1"})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
}

func TestRevokeSession_MissingSessionID(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.RevokeSession(context.Background(), 1, &userpb.RevokeSessionReq{SessionId: ""})
	assert.Error(t, err)
}

func TestRefreshToken_MissingToken(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.RefreshToken(context.Background(), &userpb.RefreshTokenReq{RefreshToken: ""})
	assert.Error(t, err)
}

func TestRefreshToken_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockAuth := new(MockAuthRepo)
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(&model.User{ID: 1, Username: "testuser"}, nil)
	mockAuth.On("IsTokenRevoked", mock.Anything, mock.Anything).Return(false, nil)

	l := newTestLogic(mockUser, mockAuth)
	// Generate a real refresh token to use
	token, err := l.jwtMgr.Generate("1", "testuser")
	assert.NoError(t, err)

	resp, err := l.RefreshToken(context.Background(), &userpb.RefreshTokenReq{RefreshToken: token})
	assert.NoError(t, err)
	assert.NotNil(t, resp.Tokens)
}

func TestRefreshToken_Revoked(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	mockAuth.On("IsTokenRevoked", mock.Anything, mock.Anything).Return(true, nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	token, err := l.jwtMgr.Generate("1", "testuser")
	assert.NoError(t, err)

	_, err = l.RefreshToken(context.Background(), &userpb.RefreshTokenReq{RefreshToken: token})
	assert.Error(t, err)
}
