//go:build integration

package integration

import (
	"strconv"
	"testing"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/logic/auth"
	"github.com/maomeng/aim/app/user-service/internal/logic/user"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAuthLogic(t *testing.T) (*auth.Logic, *user.Logic, *user.StatusLogic) {
	t.Helper()
	ctx := newUserSvcCtx(t)
	return ctx.AuthLogic, ctx.UserLogic, ctx.StatusLogic
}

func TestUserRegisterAndLogin(t *testing.T) {
	authLogic, userLogic, _ := newAuthLogic(t)

	username := "intt_" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	phone := "138" + strconv.FormatInt(time.Now().UnixMilli()%100000000, 10)
	email := username + "@test.com"

	// Register
	resp, err := authLogic.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "integration-device",
		Username: username,
		Password: "Pass1234",
		Phone:    phone,
		Email:    email,
	})
	require.NoError(t, err)
	require.NoError(t, identity.Validate(resp.UserId))
	assert.NotEmpty(t, resp.Tokens.AccessToken)
	assert.Equal(t, username, resp.User.Username)
	assert.Equal(t, phone, resp.User.Phone)
	uid := resp.UserId

	// Login by username
	r, err := authLogic.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "integration-device",
		Account:  username,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	assert.Equal(t, uid, r.UserId)
	assert.NotEmpty(t, r.Tokens.AccessToken)

	// Login with phone
	r2, err := authLogic.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "integration-device",
		Account:  phone,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	assert.Equal(t, uid, r2.UserId)

	// Login with wrong password fails
	_, err = authLogic.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "integration-device",
		Account:  username,
		Password: "wrongpass",
	})
	require.Error(t, err)

	// Get profile
	info, err := userLogic.GetProfile(userCtx(t, uid), uid)
	require.NoError(t, err)
	assert.Equal(t, username, info.Username)
	assert.Equal(t, phone, info.Phone)

	// Update profile
	avatar := "http://example.com/avatar.png"
	g := int32(1)
	bio := "hello"
	info, err = userLogic.UpdateProfile(userCtx(t, uid), uid, &userpb.UpdateProfileReq{
		Avatar: &avatar,
		Gender: &g,
		Bio:    &bio,
	})
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/avatar.png", info.Avatar)
	assert.Equal(t, int32(1), info.Gender)
	assert.Equal(t, "hello", info.Bio)

	// Get user info by ID
	info, err = userLogic.GetUserInfo(t.Context(), &userpb.GetUserInfoReq{UserId: uid})
	require.NoError(t, err)
	assert.Equal(t, username, info.Username)

	// Search user (use full unique username)
	searchResp, err := userLogic.SearchUsers(t.Context(), &userpb.SearchUsersReq{
		Keyword: username,
	})
	require.NoError(t, err)
	require.Len(t, searchResp.Users, 1)
	assert.Equal(t, username, searchResp.Users[0].Username)

	// Batch get
	batchResp, err := userLogic.BatchGetUserInfo(t.Context(), &userpb.BatchGetUserInfoReq{
		UserIds: []string{uid},
	})
	require.NoError(t, err)
	assert.Len(t, batchResp.Users, 1)

	// Duplicate username rejected
	_, err = authLogic.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "integration-device",
		Username: username,
		Password: "Pass1234",
	})
	require.Error(t, err)
}

func TestUserBatchGetStatus(t *testing.T) {
	_, _, statusLogic := newAuthLogic(t)

	resp, err := statusLogic.BatchGetStatus(t.Context(), &userpb.BatchGetStatusReq{
		UserIds: []string{"019b0123-4567-789a-bcde-0000000003e7"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Statuses, 1)
	assert.False(t, resp.Statuses[0].IsOnline)
}

func TestUserPasswordChange(t *testing.T) {
	authLogic, userLogic, _ := newAuthLogic(t)

	username := "pwdt_" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	resp, err := authLogic.Register(t.Context(), &userpb.RegisterReq{
		DeviceId: "integration-device",
		Username: username,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	uid := resp.UserId

	// Update password
	_, err = userLogic.UpdatePassword(userCtx(t, uid), uid, &userpb.UpdatePasswordReq{
		OldPassword: "Pass1234",
		NewPassword: "NewPass5678",
	})
	require.NoError(t, err)

	// Login with new password
	_, err = authLogic.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "integration-device",
		Account:  username,
		Password: "NewPass5678",
	})
	require.NoError(t, err)

	// Old password no longer works
	_, err = authLogic.Login(t.Context(), &userpb.LoginReq{
		DeviceId: "integration-device",
		Account:  username,
		Password: "Pass1234",
	})
	require.Error(t, err)
}
