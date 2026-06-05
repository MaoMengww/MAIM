//go:build integration

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/config"
	"github.com/maomeng/aim/app/user-service/internal/logic/auth"
	"github.com/maomeng/aim/app/user-service/internal/logic/user"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
)

func newAuthLogic(t *testing.T) (*auth.Logic, *user.Logic, *user.StatusLogic) {
	t.Helper()
	var c config.Config
	conf.MustLoad("../etc/user.yaml", &c)
	c.Telemetry.Endpoint = ""

	db := c.Database
	rdbCfg := c.Redis

	rdb := newRedisClient(rdbCfg.Host)
	_db := newDB(db.Driver, db.DSN)

	snowNode, err := snowflake.NewNode(1)
	require.NoError(t, err)
	jwtMgr := jwt.NewManager("test-int-key", 3600, 2592000)

	userRepo := repo.NewUserRepo(_db)
	authRepo := repo.NewAuthRepo(_db, rdb)
	return auth.New(userRepo, authRepo, snowNode, jwtMgr),
		user.New(userRepo),
		user.NewStatusLogic(rdb)
}

func TestUserRegisterAndLogin(t *testing.T) {
	authLogic, userLogic, _ := newAuthLogic(t)

	username := "intt_" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	phone := "138" + strconv.FormatInt(time.Now().UnixMilli()%100000000, 10)
	email := username + "@test.com"

	// Register
	resp, err := authLogic.Register(context.Background(), &userpb.RegisterReq{
		Username: username,
		Password: "Pass1234",
		Phone:    phone,
		Email:    email,
	})
	require.NoError(t, err)
	require.Greater(t, resp.UserId, int64(0))
	assert.NotEmpty(t, resp.Tokens.AccessToken)
	assert.Equal(t, username, resp.User.Username)
	assert.Equal(t, phone, resp.User.Phone)
	uid := resp.UserId

	// Login by username
	r, err := authLogic.Login(context.Background(), &userpb.LoginReq{
		Account:  username,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	assert.Equal(t, uid, r.UserId)
	assert.NotEmpty(t, r.Tokens.AccessToken)

	// Login with phone
	r2, err := authLogic.Login(context.Background(), &userpb.LoginReq{
		Account:  phone,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	assert.Equal(t, uid, r2.UserId)

	// Login with wrong password fails
	_, err = authLogic.Login(context.Background(), &userpb.LoginReq{
		Account:  username,
		Password: "wrongpass",
	})
	require.Error(t, err)

	// Get profile
	info, err := userLogic.GetProfile(context.Background(), uid)
	require.NoError(t, err)
	assert.Equal(t, username, info.Username)
	assert.Equal(t, phone, info.Phone)

	// Update profile
	avatar := "http://example.com/avatar.png"
	g := int32(1)
	bio := "hello"
	info, err = userLogic.UpdateProfile(context.Background(), uid, &userpb.UpdateProfileReq{
		Avatar: &avatar,
		Gender: &g,
		Bio:    &bio,
	})
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/avatar.png", info.Avatar)
	assert.Equal(t, int32(1), info.Gender)
	assert.Equal(t, "hello", info.Bio)

	// Get user info by ID
	info, err = userLogic.GetUserInfo(context.Background(), &userpb.GetUserInfoReq{UserId: uid})
	require.NoError(t, err)
	assert.Equal(t, username, info.Username)

	// Search user (use full unique username)
	searchResp, err := userLogic.SearchUsers(context.Background(), &userpb.SearchUsersReq{
		Keyword: username,
	})
	require.NoError(t, err)
	require.Len(t, searchResp.Users, 1)
	assert.Equal(t, username, searchResp.Users[0].Username)

	// Batch get
	batchResp, err := userLogic.BatchGetUserInfo(context.Background(), &userpb.BatchGetUserInfoReq{
		UserIds: []int64{uid},
	})
	require.NoError(t, err)
	assert.Len(t, batchResp.Users, 1)

	// Duplicate username rejected
	_, err = authLogic.Register(context.Background(), &userpb.RegisterReq{
		Username: username,
		Password: "Pass1234",
	})
	require.Error(t, err)
}

func TestUserBatchGetStatus(t *testing.T) {
	_, _, statusLogic := newAuthLogic(t)

	resp, err := statusLogic.BatchGetStatus(context.Background(), &userpb.BatchGetStatusReq{
		UserIds: []int64{999999999},
	})
	require.NoError(t, err)
	require.Len(t, resp.Statuses, 1)
	assert.False(t, resp.Statuses[0].IsOnline)
}

func TestUserPasswordChange(t *testing.T) {
	authLogic, userLogic, _ := newAuthLogic(t)

	username := "pwdt_" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	resp, err := authLogic.Register(context.Background(), &userpb.RegisterReq{
		Username: username,
		Password: "Pass1234",
	})
	require.NoError(t, err)
	uid := resp.UserId

	// Update password
	_, err = userLogic.UpdatePassword(context.Background(), uid, &userpb.UpdatePasswordReq{
		OldPassword: "Pass1234",
		NewPassword: "NewPass5678",
	})
	require.NoError(t, err)

	// Login with new password
	_, err = authLogic.Login(context.Background(), &userpb.LoginReq{
		Account:  username,
		Password: "NewPass5678",
	})
	require.NoError(t, err)

	// Old password no longer works
	_, err = authLogic.Login(context.Background(), &userpb.LoginReq{
		Account:  username,
		Password: "Pass1234",
	})
	require.Error(t, err)
}
