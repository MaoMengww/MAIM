package friend

import (
	"context"

	userlogic "github.com/maomeng/aim/app/user-service/internal/logic/user"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
)

// Context shares the user service's database and profile logic.
// It owns no connections and never calls UserService over RPC.
type Context struct {
	DB                *database.DB
	UserLogic         *userlogic.Logic
	FriendRequestRepo *repo.FriendRequestRepo
	FriendRepo        *repo.FriendRepo
	FriendGroupRepo   *repo.FriendGroupRepo
	BlockRepo         *repo.BlockRepo
}

func NewContext(db *database.DB, users *userlogic.Logic) *Context {
	return &Context{
		DB:                db,
		UserLogic:         users,
		FriendRequestRepo: repo.NewFriendRequestRepo(db),
		FriendRepo:        repo.NewFriendRepo(db),
		FriendGroupRepo:   repo.NewFriendGroupRepo(db),
		BlockRepo:         repo.NewBlockRepo(db),
	}
}

func userIDFromContext(ctx context.Context) string {
	userID, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(userID) != nil {
		return ""
	}
	return userID
}

func (c *Context) batchGetUserInfo(ctx context.Context, userIDs []string) (map[string]*userpb.UserInfo, error) {
	resp, err := c.UserLogic.BatchGetUserInfo(ctx, &userpb.BatchGetUserInfoReq{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	users := make(map[string]*userpb.UserInfo, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		users[user.GetId()] = user
	}
	return users, nil
}
func validateCaller(ctx context.Context, requestedUser string, ids ...string) error {
	userID := userIDFromContext(ctx)
	if userID == "" {
		return grpcError(ErrUnauthenticated)
	}
	if requestedUser != "" && requestedUser != userID {
		return grpcError(errors.New(errors.CodeForbidden, "account is not owned by caller"))
	}
	for _, id := range ids {
		if identity.Validate(id) != nil {
			return grpcError(ErrInvalidParam)
		}
	}
	return nil
}
