package auth

import (
	"context"
	"testing"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/jwt"
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

func (m *MockUserRepo) GetByID(ctx context.Context, id string) (*model.User, error) {
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

type MockAuthRepo struct {
	mock.Mock
}

func (m *MockAuthRepo) SaveDevice(ctx context.Context, d *model.UserDevice) error {
	args := m.Called(ctx, d)
	return args.Error(0)
}

func (m *MockAuthRepo) GetUserDevices(ctx context.Context, userID string) ([]*model.UserDevice, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*model.UserDevice), args.Error(1)
}

func (m *MockAuthRepo) DeleteSession(ctx context.Context, userID string, deviceID string) error {
	args := m.Called(ctx, userID, deviceID)
	return args.Error(0)
}

func (m *MockAuthRepo) GetDevice(ctx context.Context, userID string, deviceID string) (*model.UserDevice, error) {
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

func (m *MockAuthRepo) IsDeviceOnline(ctx context.Context, userID string, deviceID string) (bool, error) {
	args := m.Called(ctx, userID, deviceID)
	return args.Bool(0), args.Error(1)
}

func newTestLogic(mockUser *MockUserRepo, mockAuth *MockAuthRepo) *Logic {
	return &Logic{
		userRepo: mockUser,
		authRepo: mockAuth,
		jwtMgr:   jwt.NewManager("test-secret", 7200, 604800),
	}
}

func TestRegister_MissingUsername(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "test-device",
		Username: "",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestRegister_MissingPassword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "test-device",
		Username: "testuser",
		Password: "",
	})
	assert.Error(t, err)
}

func TestRegister_UsernameExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(&model.User{ID: "019b0123-4567-789a-bcde-000000000001"}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "test-device",
		Username: "testuser",
		Password: "password123",
	})
	assert.Error(t, err)
	mockUser.AssertExpectations(t)
}

func TestRegister_PhoneExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByPhone", mock.Anything, "13800138000").Return(&model.User{ID: "019b0123-4567-789a-bcde-000000000002"}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "test-device",
		Username: "testuser",
		Password: "password123",
		Phone:    "13800138000",
	})
	assert.Error(t, err)
}

func TestRegister_EmailExists(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByUsername", mock.Anything, "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("GetByEmail", mock.Anything, "test@example.com").Return(&model.User{ID: "019b0123-4567-789a-bcde-000000000003"}, nil)

	l := newTestLogic(mockUser, &MockAuthRepo{})
	_, err := l.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "test-device",
		Username: "testuser",
		Password: "password123",
		Email:    "test@example.com",
	})
	assert.Error(t, err)
}

func TestLogin_MissingAccount(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "test-device",
		Account:  "",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestLogin_MissingPassword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "test-device",
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
	_, err := l.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "test-device",
		Account:  "unknown",
		Password: "password123",
	})
	assert.Error(t, err)
}

func TestLogout_MissingTokenID(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.Logout(t.Context(), &userpb.LogoutReq{TokenId: ""})
	assert.Error(t, err)
}

func TestValidateToken_EmptyToken(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	resp, err := l.ValidateToken(t.Context(), &userpb.ValidateTokenReq{AccessToken: ""})
	assert.NoError(t, err)
	assert.False(t, resp.Valid)
}

func TestOAuthLogin_MissingProvider(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.OAuthLogin(t.Context(), &userpb.OAuthLoginReq{
		DeviceId: "test-device",
		Provider: "",
		Code:     "code123",
	})
	assert.Error(t, err)
}

func TestOAuthLogin_MissingCode(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.OAuthLogin(t.Context(), &userpb.OAuthLoginReq{
		DeviceId: "test-device",
		Provider: "wechat",
		Code:     "",
	})
	assert.Error(t, err)
}

func TestRevokeSession_MissingSessionID(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.RevokeSession(context.WithValue(t.Context(), interceptor.ContextKeyUserID, "019b0123-4567-789a-bcde-000000000001"), "019b0123-4567-789a-bcde-000000000001", &userpb.RevokeSessionReq{SessionId: ""})
	assert.Error(t, err)
}

func TestRefreshToken_MissingToken(t *testing.T) {
	l := newTestLogic(&MockUserRepo{}, &MockAuthRepo{})
	_, err := l.RefreshToken(t.Context(), &userpb.RefreshTokenReq{RefreshToken: ""})
	assert.Error(t, err)
}

func TestRefreshToken_Revoked(t *testing.T) {
	mockAuth := new(MockAuthRepo)
	mockAuth.On("IsTokenRevoked", mock.Anything, mock.Anything).Return(true, nil)

	l := newTestLogic(&MockUserRepo{}, mockAuth)
	token, err := l.jwtMgr.Generate("019b0123-4567-789a-bcde-000000000001", "testuser", "test-device", "019b0123-4567-789a-bcde-000000000005")
	assert.NoError(t, err)

	_, err = l.RefreshToken(t.Context(), &userpb.RefreshTokenReq{RefreshToken: token})
	assert.Error(t, err)
}
