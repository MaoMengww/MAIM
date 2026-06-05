package user

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

func TestGetUserInfo_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Username: "testuser"}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)

	l := newTestLogic(mockUser)
	resp, err := l.GetUserInfo(context.Background(), &userpb.GetUserInfoReq{UserId: 1})
	assert.NoError(t, err)
	assert.Equal(t, "testuser", resp.Username)
}

func TestGetUserInfo_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, int64(999)).Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.GetUserInfo(context.Background(), &userpb.GetUserInfoReq{UserId: 999})
	assert.Error(t, err)
}

func TestBatchGetUserInfo_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	users := []*model.User{
		{ID: 1, Username: "user1"},
		{ID: 2, Username: "user2"},
	}
	mockUser.On("BatchGetByIDs", mock.Anything, []int64{1, 2}).Return(users, nil)

	l := newTestLogic(mockUser)
	resp, err := l.BatchGetUserInfo(context.Background(), &userpb.BatchGetUserInfoReq{UserIds: []int64{1, 2}})
	assert.NoError(t, err)
	assert.Len(t, resp.Users, 2)
}

func TestBatchGetUserInfo_Empty(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	resp, err := l.BatchGetUserInfo(context.Background(), &userpb.BatchGetUserInfoReq{UserIds: []int64{}})
	assert.NoError(t, err)
	assert.Empty(t, resp.Users)
}

func TestSearchUsers_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	users := []*model.User{
		{ID: 1, Username: "test1"},
		{ID: 2, Username: "test2"},
	}
	mockUser.On("Search", mock.Anything, "test", 0, 20).Return(users, int64(2), nil)

	l := newTestLogic(mockUser)
	resp, err := l.SearchUsers(context.Background(), &userpb.SearchUsersReq{Keyword: "test"})
	assert.NoError(t, err)
	assert.Len(t, resp.Users, 2)
	assert.Equal(t, int32(1), resp.Pagination.Page)
	assert.Equal(t, int64(2), resp.Pagination.Total)
}

func TestSearchUsers_WithPagination(t *testing.T) {
	mockUser := new(MockUserRepo)
	var users []*model.User
	mockUser.On("Search", mock.Anything, "test", 20, 10).Return(users, int64(100), nil)

	l := newTestLogic(mockUser)
	resp, err := l.SearchUsers(context.Background(), &userpb.SearchUsersReq{
		Keyword: "test",
		Pagination: &commonpb.Pagination{
			Page:     3,
			PageSize: 10,
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, int32(3), resp.Pagination.Page)
	assert.Equal(t, int64(100), resp.Pagination.Total)
	assert.Equal(t, int32(10), resp.Pagination.TotalPages)
}

func TestSearchUsers_EmptyKeyword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	resp, err := l.SearchUsers(context.Background(), &userpb.SearchUsersReq{Keyword: ""})
	assert.NoError(t, err)
	assert.Empty(t, resp.Users)
}
