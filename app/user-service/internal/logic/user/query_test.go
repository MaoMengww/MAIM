package user

import (
	"testing"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

func TestGetUserInfo_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, "019b0123-4567-789a-bcde-0000000003e7").Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.GetUserInfo(t.Context(), &userpb.GetUserInfoReq{UserId: "019b0123-4567-789a-bcde-0000000003e7"})
	assert.Error(t, err)
}

func TestBatchGetUserInfo_Empty(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	resp, err := l.BatchGetUserInfo(t.Context(), &userpb.BatchGetUserInfoReq{UserIds: []string{}})
	assert.NoError(t, err)
	assert.Empty(t, resp.Users)
}

func TestSearchUsers_EmptyKeyword(t *testing.T) {
	l := newTestLogic(&MockUserRepo{})
	resp, err := l.SearchUsers(t.Context(), &userpb.SearchUsersReq{Keyword: ""})
	assert.NoError(t, err)
	assert.Empty(t, resp.Users)
}
