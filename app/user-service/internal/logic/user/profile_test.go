package user

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/crypto/bcrypt"
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

func (m *MockUserRepo) Update(ctx context.Context, id int64, updates map[string]any) error {
	args := m.Called(ctx, id, updates)
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

func (m *MockUserRepo) UpdateBalance(ctx context.Context, userID int64, delta float64) (float64, error) {
	args := m.Called(ctx, userID, delta)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockUserRepo) GetBalance(ctx context.Context, userID int64) (float64, error) {
	args := m.Called(ctx, userID)
	return args.Get(0).(float64), args.Error(1)
}

func newTestLogic(mockUser *MockUserRepo) *Logic {
	return &Logic{userRepo: mockUser}
}

func TestGetProfile_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Username: "test", Email: "test@example.com"}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)

	l := newTestLogic(mockUser)
	resp, err := l.GetProfile(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "test", resp.Username)
}

func TestGetProfile_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, int64(999)).Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.GetProfile(context.Background(), 999)
	assert.Error(t, err)
}

func TestUpdateProfile_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Username: "test"}
	avatar := "https://example.com/avatar.png"
	gender := int32(1)
	bio := "hello"
	birthday := int64(946684800)
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("Update", mock.Anything, int64(1), mock.MatchedBy(func(updates map[string]any) bool {
		return updates["avatar"] == avatar && updates["gender"] == gender &&
			updates["bio"] == bio && updates["birthday"] == birthday
	})).Return(nil)

	l := newTestLogic(mockUser)
	resp, err := l.UpdateProfile(context.Background(), 1, &userpb.UpdateProfileReq{
		Avatar:   &avatar,
		Gender:   &gender,
		Bio:      &bio,
		Birthday: &birthday,
	})
	assert.NoError(t, err)
	assert.Equal(t, avatar, resp.Avatar)
	assert.Equal(t, gender, resp.Gender)
	assert.Equal(t, bio, resp.Bio)
	assert.Equal(t, birthday, resp.Birthday)
}

func TestUpdateProfile_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, int64(999)).Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.UpdateProfile(context.Background(), 999, &userpb.UpdateProfileReq{})
	assert.Error(t, err)
}

func TestUpdatePassword_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpass"), bcrypt.DefaultCost)
	u := &model.User{ID: 1, PasswordHash: string(hash)}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("Update", mock.Anything, int64(1), mock.MatchedBy(func(updates map[string]any) bool {
		_, ok := updates["password_hash"].(string)
		return ok
	})).Return(nil)

	l := newTestLogic(mockUser)
	resp, err := l.UpdatePassword(context.Background(), 1, &userpb.UpdatePasswordReq{
		OldPassword: "oldpass",
		NewPassword: "newpass",
	})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
}

func TestUpdatePassword_WrongOldPassword(t *testing.T) {
	mockUser := new(MockUserRepo)
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.DefaultCost)
	u := &model.User{ID: 1, PasswordHash: string(hash)}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)

	l := newTestLogic(mockUser)
	_, err := l.UpdatePassword(context.Background(), 1, &userpb.UpdatePasswordReq{
		OldPassword: "wrong",
		NewPassword: "newpass",
	})
	assert.Error(t, err)
}

func TestUpdatePassword_MissingFields(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	_, err := l.UpdatePassword(context.Background(), 1, &userpb.UpdatePasswordReq{
		OldPassword: "",
		NewPassword: "",
	})
	assert.Error(t, err)
}

func TestBindPhone_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Phone: ""}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("GetByPhone", mock.Anything, "13800138000").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("Update", mock.Anything, int64(1), mock.Anything).Return(nil)

	l := newTestLogic(mockUser)
	resp, err := l.BindPhone(context.Background(), 1, &userpb.BindPhoneReq{Phone: "13800138000"})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
}

func TestBindPhone_AlreadyBound(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Phone: ""}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("GetByPhone", mock.Anything, "13800138000").Return(&model.User{ID: 2}, nil)

	l := newTestLogic(mockUser)
	_, err := l.BindPhone(context.Background(), 1, &userpb.BindPhoneReq{Phone: "13800138000"})
	assert.Error(t, err)
}

func TestBindPhone_MissingPhone(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	_, err := l.BindPhone(context.Background(), 1, &userpb.BindPhoneReq{Phone: ""})
	assert.Error(t, err)
}

func TestBindEmail_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Email: ""}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("GetByEmail", mock.Anything, "new@example.com").Return(nil, gorm.ErrRecordNotFound)
	mockUser.On("Update", mock.Anything, int64(1), mock.Anything).Return(nil)

	l := newTestLogic(mockUser)
	resp, err := l.BindEmail(context.Background(), 1, &userpb.BindEmailReq{Email: "new@example.com"})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
}

func TestBindEmail_AlreadyBound(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Email: ""}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("GetByEmail", mock.Anything, "used@example.com").Return(&model.User{ID: 2}, nil)

	l := newTestLogic(mockUser)
	_, err := l.BindEmail(context.Background(), 1, &userpb.BindEmailReq{Email: "used@example.com"})
	assert.Error(t, err)
}

func TestBindEmail_MissingEmail(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	_, err := l.BindEmail(context.Background(), 1, &userpb.BindEmailReq{Email: ""})
	assert.Error(t, err)
}
