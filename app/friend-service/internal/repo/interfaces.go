package repo

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/model"
)

type FriendRequestRepoInterface interface {
	Create(ctx context.Context, req *model.FriendRequest) error
	GetByID(ctx context.Context, id int64) (*model.FriendRequest, error)
	UpdateStatus(ctx context.Context, id int64, status int32) error
	ListPending(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error)
	ListSent(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error)
	CheckPending(ctx context.Context, fromUserID, toUserID int64) (bool, error)
}

type FriendRepoInterface interface {
	CreatePair(ctx context.Context, userID, friendID, groupID int64, genID func() int64) error
	DeletePair(ctx context.Context, userID, friendID int64) error
	GetRelation(ctx context.Context, userID, friendID int64) (*model.Friend, error)
	List(ctx context.Context, userID int64, groupID *int64, offset, limit int) ([]model.Friend, int64, error)
	UpdateRemark(ctx context.Context, userID, friendID int64, remark string) error
	UpdateGroup(ctx context.Context, userID, friendID, groupID int64) error
	IsFriend(ctx context.Context, userID, targetID int64) (bool, error)
}

type FriendGroupRepoInterface interface {
	Create(ctx context.Context, userID int64, name string) (int64, error)
	Update(ctx context.Context, id int64, name string) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, userID int64) ([]model.FriendGroup, error)
	GetByID(ctx context.Context, id int64) (*model.FriendGroup, error)
}

type BlockRepoInterface interface {
	Create(ctx context.Context, userID, blockedUserID int64) error
	Delete(ctx context.Context, userID, blockedUserID int64) error
	List(ctx context.Context, userID int64, offset, limit int) ([]model.UserBlock, int64, error)
	IsBlocked(ctx context.Context, userID, targetID int64) (bool, error)
}
