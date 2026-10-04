package friend

import (
	"context"

	userlogic "github.com/maomeng/aim/app/user-service/internal/logic/user"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/snowflake"
)

// Context shares the user service's database, ID generator and profile logic.
// It owns no connections and never calls UserService over RPC.
type Context struct {
	DB                *database.DB
	Snowflake         *snowflake.Node
	UserLogic         *userlogic.Logic
	FriendRequestRepo *repo.FriendRequestRepo
	FriendRepo        *repo.FriendRepo
	FriendGroupRepo   *repo.FriendGroupRepo
	BlockRepo         *repo.BlockRepo
}

func NewContext(db *database.DB, snow *snowflake.Node, users *userlogic.Logic) *Context {
	return &Context{
		DB:                db,
		Snowflake:         snow,
		UserLogic:         users,
		FriendRequestRepo: repo.NewFriendRequestRepo(db),
		FriendRepo:        repo.NewFriendRepo(db),
		FriendGroupRepo:   repo.NewFriendGroupRepo(db),
		BlockRepo:         repo.NewBlockRepo(db),
	}
}

func userIDFromContext(ctx context.Context) int64 {
	userID, _ := ctx.Value(interceptor.ContextKeyUserID).(int64)
	return userID
}

func (c *Context) batchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*userpb.UserInfo, error) {
	resp, err := c.UserLogic.BatchGetUserInfo(ctx, &userpb.BatchGetUserInfoReq{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	users := make(map[int64]*userpb.UserInfo, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		users[user.GetId()] = user
	}
	return users, nil
}
