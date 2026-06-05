package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maomeng/aim/app/friend-service/internal/model"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	pbcommon "github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/snowflake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSvcCtx() *svc.ServiceContext {
	sn, _ := snowflake.NewNode(1)
	return &svc.ServiceContext{
		Snowflake:         sn,
		UserClient:        &mockUserClient{},
		FriendRequestRepo: newMockFriendRequestRepo(),
		FriendRepo:        &mockFriendRepo{},
		FriendGroupRepo:   &mockFriendGroupRepo{},
		BlockRepo:         &mockBlockRepo{},
	}
}

func ctxWithUserID(uid int64) context.Context {
	return context.WithValue(context.Background(), svc.CtxKeyUserID, uid)
}

// ========== SendRequest Tests ==========

func TestSendRequest_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewSendRequestLogic(ctx, svcCtx)

	resp, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002, Message: "hello"})
	require.NoError(t, err)
	assert.Greater(t, resp.RequestId, int64(0))
}

func TestSendRequest_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewSendRequestLogic(context.Background(), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002})
	assert.Error(t, err)
}

func TestSendRequest_SelfFriend(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewSendRequestLogic(ctxWithUserID(1001), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1001})
	assert.Error(t, err)
}

func TestSendRequest_AlreadyFriend(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	logic := NewSendRequestLogic(ctxWithUserID(1001), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002})
	assert.Error(t, err)
}

func TestSendRequest_Blocked(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{isBlocked: true}
	logic := NewSendRequestLogic(ctxWithUserID(1001), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002})
	assert.Error(t, err)
}

func TestSendRequest_AlreadyPending(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{hasPending: true}
	logic := NewSendRequestLogic(ctxWithUserID(1001), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002})
	assert.Error(t, err)
}

func TestSendRequest_CreateError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{createErr: errors.New("db error")}
	logic := NewSendRequestLogic(ctxWithUserID(1001), svcCtx)

	_, err := logic.SendRequest(&friend.SendRequestReq{ToUserId: 1002, Message: "hello"})
	assert.Error(t, err)
}

// ========== AcceptRequest Tests ==========

func TestAcceptRequest_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1002) // recipient
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	resp, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestAcceptRequest_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewAcceptRequestLogic(context.Background(), svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestAcceptRequest_NotRecipient(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(9999) // not the recipient
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestAcceptRequest_AlreadyHandled(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{status: model.FriendRequestStatusAccepted}
	ctx := ctxWithUserID(1002)
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestAcceptRequest_RequestNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{getByIDErr: errors.New("not found")}
	ctx := ctxWithUserID(1002)
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 999})
	assert.Error(t, err)
}

func TestAcceptRequest_UpdateStatusError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{updateStatusErr: errors.New("update failed")}
	ctx := ctxWithUserID(1002)
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestAcceptRequest_CreatePairError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{createPairErr: errors.New("create pair failed")}
	ctx := ctxWithUserID(1002)
	logic := NewAcceptRequestLogic(ctx, svcCtx)

	_, err := logic.AcceptRequest(&friend.AcceptRequestReq{RequestId: 1})
	assert.Error(t, err)
}

// ========== RejectRequest Tests ==========

func TestRejectRequest_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1002)
	logic := NewRejectRequestLogic(ctx, svcCtx)

	resp, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestRejectRequest_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewRejectRequestLogic(context.Background(), svcCtx)

	_, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestRejectRequest_NotRecipient(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(9999)
	logic := NewRejectRequestLogic(ctx, svcCtx)

	_, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestRejectRequest_AlreadyHandled(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{status: model.FriendRequestStatusAccepted}
	ctx := ctxWithUserID(1002)
	logic := NewRejectRequestLogic(ctx, svcCtx)

	_, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestRejectRequest_RequestNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{getByIDErr: errors.New("not found")}
	ctx := ctxWithUserID(1002)
	logic := NewRejectRequestLogic(ctx, svcCtx)

	_, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 999})
	assert.Error(t, err)
}

func TestRejectRequest_UpdateStatusError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{updateStatusErr: errors.New("update failed")}
	ctx := ctxWithUserID(1002)
	logic := NewRejectRequestLogic(ctx, svcCtx)

	_, err := logic.RejectRequest(&friend.RejectRequestReq{RequestId: 1})
	assert.Error(t, err)
}

// ========== CancelRequest Tests ==========

func TestCancelRequest_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001) // sender
	logic := NewCancelRequestLogic(ctx, svcCtx)

	resp, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestCancelRequest_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewCancelRequestLogic(context.Background(), svcCtx)

	_, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestCancelRequest_NotSender(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(9999)
	logic := NewCancelRequestLogic(ctx, svcCtx)

	_, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestCancelRequest_AlreadyHandled(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{status: model.FriendRequestStatusAccepted}
	ctx := ctxWithUserID(1001)
	logic := NewCancelRequestLogic(ctx, svcCtx)

	_, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 1})
	assert.Error(t, err)
}

func TestCancelRequest_RequestNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{getByIDErr: errors.New("not found")}
	ctx := ctxWithUserID(1001)
	logic := NewCancelRequestLogic(ctx, svcCtx)

	_, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 999})
	assert.Error(t, err)
}

func TestCancelRequest_UpdateStatusError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{updateStatusErr: errors.New("update failed")}
	ctx := ctxWithUserID(1001)
	logic := NewCancelRequestLogic(ctx, svcCtx)

	_, err := logic.CancelRequest(&friend.CancelRequestReq{RequestId: 1})
	assert.Error(t, err)
}

// ========== ListPendingRequests Tests ==========

func TestListPendingRequests_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1002)
	logic := NewListPendingRequestsLogic(ctx, svcCtx)

	resp, err := logic.ListPendingRequests(&friend.ListPendingRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Pagination)
	assert.Equal(t, int64(1), resp.Pagination.Total)
}

func TestListPendingRequests_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewListPendingRequestsLogic(context.Background(), svcCtx)

	_, err := logic.ListPendingRequests(&friend.ListPendingRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
}

func TestListPendingRequests_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{listEmpty: true}
	svcCtx.UserClient = &mockUserClient{}
	ctx := ctxWithUserID(1002)
	logic := NewListPendingRequestsLogic(ctx, svcCtx)

	resp, err := logic.ListPendingRequests(&friend.ListPendingRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Requests)
	assert.Equal(t, int64(0), resp.Pagination.Total)
}

func TestListPendingRequests_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{listErr: errors.New("db error")}
	ctx := ctxWithUserID(1002)
	logic := NewListPendingRequestsLogic(ctx, svcCtx)

	_, err := logic.ListPendingRequests(&friend.ListPendingRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
}

// ========== ListSentRequests Tests ==========

func TestListSentRequests_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewListSentRequestsLogic(ctx, svcCtx)

	resp, err := logic.ListSentRequests(&friend.ListSentRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Pagination)
}

func TestListSentRequests_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewListSentRequestsLogic(context.Background(), svcCtx)

	_, err := logic.ListSentRequests(&friend.ListSentRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
}

func TestListSentRequests_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{listEmpty: true}
	ctx := ctxWithUserID(1001)
	logic := NewListSentRequestsLogic(ctx, svcCtx)

	resp, err := logic.ListSentRequests(&friend.ListSentRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Requests)
	assert.Equal(t, int64(0), resp.Pagination.Total)
}

func TestListSentRequests_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRequestRepo = &mockFriendRequestRepo{listErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewListSentRequestsLogic(ctx, svcCtx)

	_, err := logic.ListSentRequests(&friend.ListSentRequestsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
}

// ========== ListFriends Tests ==========

func TestListFriends_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewListFriendsLogic(ctx, svcCtx)

	resp, err := logic.ListFriends(&friend.ListFriendsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Friends)
	assert.NotNil(t, resp.Pagination)
}

func TestListFriends_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewListFriendsLogic(context.Background(), svcCtx)

	_, err := logic.ListFriends(&friend.ListFriendsReq{})
	assert.Error(t, err)
}

func TestListFriends_ByGroup(t *testing.T) {
	svcCtx := newTestSvcCtx()
	groupID := int64(1)
	ctx := ctxWithUserID(1001)
	logic := NewListFriendsLogic(ctx, svcCtx)

	resp, err := logic.ListFriends(&friend.ListFriendsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
		GroupId:    &groupID,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Friends)
}

func TestListFriends_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{listEmpty: true}
	ctx := ctxWithUserID(1001)
	logic := NewListFriendsLogic(ctx, svcCtx)

	resp, err := logic.ListFriends(&friend.ListFriendsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Friends)
	assert.Equal(t, int64(0), resp.Pagination.Total)
}

func TestListFriends_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{listErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewListFriendsLogic(ctx, svcCtx)

	_, err := logic.ListFriends(&friend.ListFriendsReq{
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
}

// ========== DeleteFriend Tests ==========

func TestDeleteFriend_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	ctx := ctxWithUserID(1001)
	logic := NewDeleteFriendLogic(ctx, svcCtx)

	resp, err := logic.DeleteFriend(&friend.DeleteFriendReq{FriendId: 1002})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestDeleteFriend_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	logic := NewDeleteFriendLogic(context.Background(), svcCtx)

	_, err := logic.DeleteFriend(&friend.DeleteFriendReq{FriendId: 1002})
	assert.Error(t, err)
}

func TestDeleteFriend_NotFriend(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewDeleteFriendLogic(ctx, svcCtx)

	_, err := logic.DeleteFriend(&friend.DeleteFriendReq{FriendId: 9999})
	assert.Error(t, err)
}

func TestDeleteFriend_DeleteError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true, deletePairErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewDeleteFriendLogic(ctx, svcCtx)

	_, err := logic.DeleteFriend(&friend.DeleteFriendReq{FriendId: 1002})
	assert.Error(t, err)
}

// ========== SetRemark Tests ==========

func TestSetRemark_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	ctx := ctxWithUserID(1001)
	logic := NewSetRemarkLogic(ctx, svcCtx)

	resp, err := logic.SetRemark(&friend.SetRemarkReq{FriendId: 1002, Remark: "best friend"})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestSetRemark_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewSetRemarkLogic(context.Background(), svcCtx)

	_, err := logic.SetRemark(&friend.SetRemarkReq{FriendId: 1002, Remark: "best friend"})
	assert.Error(t, err)
}

func TestSetRemark_NotFriend(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewSetRemarkLogic(ctx, svcCtx)

	_, err := logic.SetRemark(&friend.SetRemarkReq{FriendId: 9999, Remark: "stranger"})
	assert.Error(t, err)
}

func TestSetRemark_UpdateError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true, updateRemarkErr: errors.New("update failed")}
	ctx := ctxWithUserID(1001)
	logic := NewSetRemarkLogic(ctx, svcCtx)

	_, err := logic.SetRemark(&friend.SetRemarkReq{FriendId: 1002, Remark: "buddy"})
	assert.Error(t, err)
}

// ========== Group Tests ==========

func TestCreateGroup_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewCreateGroupLogic(ctx, svcCtx)

	resp, err := logic.CreateGroup(&friend.CreateGroupReq{Name: "close friends"})
	require.NoError(t, err)
	assert.Greater(t, resp.GroupId, int64(0))
}

func TestCreateGroup_EmptyName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewCreateGroupLogic(ctx, svcCtx)

	_, err := logic.CreateGroup(&friend.CreateGroupReq{Name: ""})
	assert.Error(t, err)
}

func TestCreateGroup_WhitespaceName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewCreateGroupLogic(ctx, svcCtx)

	_, err := logic.CreateGroup(&friend.CreateGroupReq{Name: "   "})
	assert.Error(t, err)
}

func TestCreateGroup_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewCreateGroupLogic(context.Background(), svcCtx)

	_, err := logic.CreateGroup(&friend.CreateGroupReq{Name: "group"})
	assert.Error(t, err)
}

func TestCreateGroup_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{createErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewCreateGroupLogic(ctx, svcCtx)

	_, err := logic.CreateGroup(&friend.CreateGroupReq{Name: "group"})
	assert.Error(t, err)
}

func TestRenameGroup_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	resp, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: "besties"})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestRenameGroup_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewRenameGroupLogic(context.Background(), svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: "besties"})
	assert.Error(t, err)
}

func TestRenameGroup_EmptyName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: ""})
	assert.Error(t, err)
}

func TestRenameGroup_WhitespaceName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: "   "})
	assert.Error(t, err)
}

func TestRenameGroup_NotOwner(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{wrongOwner: true}
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: "hacked"})
	assert.Error(t, err)
}

func TestRenameGroup_GroupNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{notFound: true}
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 999, Name: "ghost"})
	assert.Error(t, err)
}

func TestRenameGroup_UpdateError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{updateErr: errors.New("update failed")}
	ctx := ctxWithUserID(1001)
	logic := NewRenameGroupLogic(ctx, svcCtx)

	_, err := logic.RenameGroup(&friend.RenameGroupReq{GroupId: 1, Name: "newname"})
	assert.Error(t, err)
}

func TestDeleteGroup_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewDeleteGroupLogic(ctx, svcCtx)

	resp, err := logic.DeleteGroup(&friend.DeleteGroupReq{GroupId: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestDeleteGroup_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewDeleteGroupLogic(context.Background(), svcCtx)

	_, err := logic.DeleteGroup(&friend.DeleteGroupReq{GroupId: 1})
	assert.Error(t, err)
}

func TestDeleteGroup_GroupNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{notFound: true}
	ctx := ctxWithUserID(1001)
	logic := NewDeleteGroupLogic(ctx, svcCtx)

	_, err := logic.DeleteGroup(&friend.DeleteGroupReq{GroupId: 999})
	assert.Error(t, err)
}

func TestDeleteGroup_NotOwner(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{wrongOwner: true}
	ctx := ctxWithUserID(1001)
	logic := NewDeleteGroupLogic(ctx, svcCtx)

	_, err := logic.DeleteGroup(&friend.DeleteGroupReq{GroupId: 1})
	assert.Error(t, err)
}

func TestDeleteGroup_DeleteError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{deleteErr: errors.New("delete failed")}
	ctx := ctxWithUserID(1001)
	logic := NewDeleteGroupLogic(ctx, svcCtx)

	_, err := logic.DeleteGroup(&friend.DeleteGroupReq{GroupId: 1})
	assert.Error(t, err)
}

func TestListGroups_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewListGroupsLogic(ctx, svcCtx)

	resp, err := logic.ListGroups(&friend.ListGroupsReq{})
	require.NoError(t, err)
	assert.NotNil(t, resp.Groups)
}

func TestListGroups_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewListGroupsLogic(context.Background(), svcCtx)

	_, err := logic.ListGroups(&friend.ListGroupsReq{})
	assert.Error(t, err)
}

func TestListGroups_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{listErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewListGroupsLogic(ctx, svcCtx)

	_, err := logic.ListGroups(&friend.ListGroupsReq{})
	assert.Error(t, err)
}

// ========== Block / Unblock Tests ==========

func TestBlockUser_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	ctx := ctxWithUserID(1001)
	logic := NewBlockUserLogic(ctx, svcCtx)

	resp, err := logic.BlockUser(&friend.BlockUserReq{BlockedUserId: 1002})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestBlockUser_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBlockUserLogic(context.Background(), svcCtx)

	_, err := logic.BlockUser(&friend.BlockUserReq{BlockedUserId: 1002})
	assert.Error(t, err)
}

func TestBlockUser_SelfBlock(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewBlockUserLogic(ctx, svcCtx)

	_, err := logic.BlockUser(&friend.BlockUserReq{BlockedUserId: 1001})
	assert.Error(t, err)
}

func TestBlockUser_AlreadyBlocked(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{isBlocked: true}
	ctx := ctxWithUserID(1001)
	logic := NewBlockUserLogic(ctx, svcCtx)

	// Already blocked is idempotent - returns success
	resp, err := logic.BlockUser(&friend.BlockUserReq{BlockedUserId: 1002})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestBlockUser_CreateError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{createErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewBlockUserLogic(ctx, svcCtx)

	_, err := logic.BlockUser(&friend.BlockUserReq{BlockedUserId: 1002})
	assert.Error(t, err)
}

func TestUnblockUser_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewUnblockUserLogic(ctx, svcCtx)

	resp, err := logic.UnblockUser(&friend.UnblockUserReq{BlockedUserId: 1002})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestUnblockUser_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUnblockUserLogic(context.Background(), svcCtx)

	_, err := logic.UnblockUser(&friend.UnblockUserReq{BlockedUserId: 1002})
	assert.Error(t, err)
}

func TestUnblockUser_DeleteError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{deleteErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewUnblockUserLogic(ctx, svcCtx)

	_, err := logic.UnblockUser(&friend.UnblockUserReq{BlockedUserId: 1002})
	assert.Error(t, err)
}

func TestListBlacklist_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewListBlacklistLogic(ctx, svcCtx)

	resp, err := logic.ListBlacklist(&friend.ListBlacklistReq{Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20}})
	require.NoError(t, err)
	assert.NotNil(t, resp.Users)
	assert.NotNil(t, resp.Pagination)
}

func TestListBlacklist_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewListBlacklistLogic(context.Background(), svcCtx)

	_, err := logic.ListBlacklist(&friend.ListBlacklistReq{Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20}})
	assert.Error(t, err)
}

func TestListBlacklist_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{listEmpty: true}
	ctx := ctxWithUserID(1001)
	logic := NewListBlacklistLogic(ctx, svcCtx)

	resp, err := logic.ListBlacklist(&friend.ListBlacklistReq{Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20}})
	require.NoError(t, err)
	assert.Empty(t, resp.Users)
	assert.Equal(t, int64(0), resp.Pagination.Total)
}

func TestListBlacklist_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{listErr: errors.New("db error")}
	ctx := ctxWithUserID(1001)
	logic := NewListBlacklistLogic(ctx, svcCtx)

	_, err := logic.ListBlacklist(&friend.ListBlacklistReq{Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20}})
	assert.Error(t, err)
}

func TestIsBlocked_True(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{isBlocked: true}
	ctx := ctxWithUserID(1001)
	logic := NewIsBlockedLogic(ctx, svcCtx)

	resp, err := logic.IsBlocked(&friend.IsBlockedReq{UserId: 1001, TargetUserId: 1002})
	require.NoError(t, err)
	assert.True(t, resp.IsBlocked)
}

func TestIsBlocked_False(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewIsBlockedLogic(ctx, svcCtx)

	resp, err := logic.IsBlocked(&friend.IsBlockedReq{UserId: 1001, TargetUserId: 1002})
	require.NoError(t, err)
	assert.False(t, resp.IsBlocked)
}

func TestIsBlocked_InvalidParam(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewIsBlockedLogic(context.Background(), svcCtx)

	_, err := logic.IsBlocked(&friend.IsBlockedReq{UserId: 0, TargetUserId: 1002})
	assert.Error(t, err)
}

func TestIsBlocked_RepoError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.BlockRepo = &mockBlockRepo{isBlockedErr: errors.New("db error")}
	logic := NewIsBlockedLogic(context.Background(), svcCtx)

	_, err := logic.IsBlocked(&friend.IsBlockedReq{UserId: 1001, TargetUserId: 1002})
	assert.Error(t, err)
}

// ========== SetGroup Tests ==========

func TestSetGroup_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	resp, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestSetGroup_Unauthenticated(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewSetGroupLogic(context.Background(), svcCtx)

	_, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 1})
	assert.Error(t, err)
}

func TestSetGroup_NotFriend(t *testing.T) {
	svcCtx := newTestSvcCtx()
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	_, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 9999, GroupId: 1})
	assert.Error(t, err)
}

func TestSetGroup_GroupNotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{notFound: true}
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	_, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 999})
	assert.Error(t, err)
}

func TestSetGroup_WrongGroupOwner(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	svcCtx.FriendGroupRepo = &mockFriendGroupRepo{wrongOwner: true}
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	_, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 1})
	assert.Error(t, err)
}

func TestSetGroup_ClearGroup(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true}
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	// Setting group to 0 should succeed (clears group assignment)
	resp, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 0})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestSetGroup_UpdateError(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FriendRepo = &mockFriendRepo{isFriend: true, updateGroupErr: errors.New("update failed")}
	ctx := ctxWithUserID(1001)
	logic := NewSetGroupLogic(ctx, svcCtx)

	_, err := logic.SetGroup(&friend.SetGroupReq{FriendId: 1002, GroupId: 1})
	assert.Error(t, err)
}

// ========== Helper Tests ==========

func TestPaginationParams_Nil(t *testing.T) {
	offset, limit := paginationParams(nil)
	assert.Equal(t, 0, offset)
	assert.Equal(t, 20, limit)
}

func TestPaginationParams_Default(t *testing.T) {
	offset, limit := paginationParams(&pbcommon.Pagination{Page: -1, PageSize: 200})
	assert.Equal(t, 0, offset)
	assert.Equal(t, 20, limit)
}

func TestPaginationParams_Exact(t *testing.T) {
	offset, limit := paginationParams(&pbcommon.Pagination{Page: 3, PageSize: 10})
	assert.Equal(t, 20, offset)
	assert.Equal(t, 10, limit)
}

func TestPaginationResp(t *testing.T) {
	resp := paginationResp(1, 20, 55)
	assert.Equal(t, int32(1), resp.Page)
	assert.Equal(t, int32(20), resp.PageSize)
	assert.Equal(t, int64(55), resp.Total)
	assert.Equal(t, int32(3), resp.TotalPages)
}

func TestPaginationResp_Zero(t *testing.T) {
	resp := paginationResp(1, 0, 0)
	assert.Equal(t, int32(0), resp.TotalPages)
}

func TestPaginationResp_ExactMultiple(t *testing.T) {
	resp := paginationResp(1, 10, 30)
	assert.Equal(t, int32(3), resp.TotalPages)
}

func TestGrpcError_Nil(t *testing.T) {
	assert.Nil(t, grpcError(nil))
}

func TestGrpcError_DefaultBranch(t *testing.T) {
	// BizError with code not in 1001-1005 hits the default branch
	unknownBizErr := pkg_errors.New(9999, "unknown biz error")
	err := grpcError(unknownBizErr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown biz error")
}

func TestGrpcError_Mapping(t *testing.T) {
	assert.NotNil(t, grpcError(ErrUnauthenticated))
	assert.NotNil(t, grpcError(ErrInvalidParam))
	assert.NotNil(t, grpcError(ErrRequestNotFound))
	assert.NotNil(t, grpcError(ErrAlreadyFriend))
	assert.NotNil(t, grpcError(ErrNotGroupOwner))
	assert.NotNil(t, grpcError(ErrBlocked))
	// unknown error maps to internal
	assert.NotNil(t, grpcError(assert.AnError))
}

func TestUsernameFromUser_Nil(t *testing.T) {
	assert.Equal(t, "", usernameFromUser(nil))
}

func TestUsernameFromUser_Valid(t *testing.T) {
	assert.Equal(t, "alice", usernameFromUser(&userpb.UserInfo{Username: "alice"}))
}

func TestAvatarFromUser_Nil(t *testing.T) {
	assert.Equal(t, "", avatarFromUser(nil))
}

func TestAvatarFromUser_Valid(t *testing.T) {
	assert.Equal(t, "avatar.png", avatarFromUser(&userpb.UserInfo{Avatar: "avatar.png"}))
}

func TestStatusToString(t *testing.T) {
	assert.Equal(t, "pending", statusToString(model.FriendRequestStatusPending))
	assert.Equal(t, "accepted", statusToString(model.FriendRequestStatusAccepted))
	assert.Equal(t, "rejected", statusToString(model.FriendRequestStatusRejected))
	assert.Equal(t, "cancelled", statusToString(model.FriendRequestStatusCancelled))
	assert.Equal(t, "unknown", statusToString(99))
}

// ========== Mock Implementations ==========

type mockFriendRequestRepo struct {
	hasPending      bool
	status          int32
	createErr       error
	updateStatusErr error
	getByIDErr      error
	listErr         error
	listEmpty       bool
}

func newMockFriendRequestRepo() *mockFriendRequestRepo {
	return &mockFriendRequestRepo{status: model.FriendRequestStatusPending}
}

func (m *mockFriendRequestRepo) Create(ctx context.Context, req *model.FriendRequest) error {
	return m.createErr
}

func (m *mockFriendRequestRepo) GetByID(ctx context.Context, id int64) (*model.FriendRequest, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	st := m.status
	if st == 0 {
		st = model.FriendRequestStatusPending
	}
	return &model.FriendRequest{
		ID:         id,
		FromUserID: 1001,
		ToUserID:   1002,
		Message:    "hello",
		Status:     st,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (m *mockFriendRequestRepo) UpdateStatus(ctx context.Context, id int64, status int32) error {
	return m.updateStatusErr
}

func (m *mockFriendRequestRepo) ListPending(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	if m.listEmpty {
		return nil, 0, nil
	}
	return []model.FriendRequest{{ID: 1, FromUserID: 1001, ToUserID: 1002, Status: model.FriendRequestStatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}}, 1, nil
}

func (m *mockFriendRequestRepo) ListSent(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	if m.listEmpty {
		return nil, 0, nil
	}
	return []model.FriendRequest{{ID: 1, FromUserID: 1001, ToUserID: 1002, Status: model.FriendRequestStatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}}, 1, nil
}

func (m *mockFriendRequestRepo) CheckPending(ctx context.Context, fromUserID, toUserID int64) (bool, error) {
	return m.hasPending, nil
}

type mockFriendRepo struct {
	isFriend        bool
	createPairErr   error
	deletePairErr   error
	listErr         error
	updateRemarkErr error
	updateGroupErr  error
	listEmpty       bool
}

func (m *mockFriendRepo) CreatePair(ctx context.Context, userID, friendID, groupID int64, _ func() int64) error {
	return m.createPairErr
}

func (m *mockFriendRepo) DeletePair(ctx context.Context, userID, friendID int64) error {
	return m.deletePairErr
}

func (m *mockFriendRepo) GetRelation(ctx context.Context, userID, friendID int64) (*model.Friend, error) {
	return &model.Friend{ID: 1, UserID: userID, FriendID: friendID, CreatedAt: time.Now()}, nil
}

func (m *mockFriendRepo) List(ctx context.Context, userID int64, groupID *int64, offset, limit int) ([]model.Friend, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	if m.listEmpty {
		return nil, 0, nil
	}
	return []model.Friend{{ID: 1, UserID: userID, FriendID: 1002, GroupID: 1, CreatedAt: time.Now()}}, 1, nil
}

func (m *mockFriendRepo) UpdateRemark(ctx context.Context, userID, friendID int64, remark string) error {
	return m.updateRemarkErr
}

func (m *mockFriendRepo) UpdateGroup(ctx context.Context, userID, friendID, groupID int64) error {
	return m.updateGroupErr
}

func (m *mockFriendRepo) IsFriend(ctx context.Context, userID, targetID int64) (bool, error) {
	return m.isFriend, nil
}

type mockFriendGroupRepo struct {
	wrongOwner bool
	notFound   bool
	createErr  error
	updateErr  error
	deleteErr  error
	listErr    error
}

func (m *mockFriendGroupRepo) Create(ctx context.Context, userID int64, name string) (int64, error) {
	if m.createErr != nil {
		return 0, m.createErr
	}
	return 100, nil
}

func (m *mockFriendGroupRepo) Update(ctx context.Context, id int64, name string) error {
	return m.updateErr
}

func (m *mockFriendGroupRepo) Delete(ctx context.Context, id int64) error {
	return m.deleteErr
}

func (m *mockFriendGroupRepo) List(ctx context.Context, userID int64) ([]model.FriendGroup, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return []model.FriendGroup{{ID: 1, UserID: userID, Name: "close friends"}}, nil
}

func (m *mockFriendGroupRepo) GetByID(ctx context.Context, id int64) (*model.FriendGroup, error) {
	if m.notFound {
		return nil, assert.AnError
	}
	ownerID := int64(1001)
	if m.wrongOwner {
		ownerID = 9999
	}
	return &model.FriendGroup{ID: id, UserID: ownerID, Name: "test group"}, nil
}

type mockBlockRepo struct {
	isBlocked    bool
	createErr    error
	deleteErr    error
	listErr      error
	isBlockedErr error
	listEmpty    bool
}

func (m *mockBlockRepo) Create(ctx context.Context, userID, blockedUserID int64) error {
	return m.createErr
}

func (m *mockBlockRepo) Delete(ctx context.Context, userID, blockedUserID int64) error {
	return m.deleteErr
}

func (m *mockBlockRepo) List(ctx context.Context, userID int64, offset, limit int) ([]model.UserBlock, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	if m.listEmpty {
		return nil, 0, nil
	}
	return []model.UserBlock{{ID: 1, UserID: userID, BlockedUserID: 1002, CreatedAt: time.Now()}}, 1, nil
}

func (m *mockBlockRepo) IsBlocked(ctx context.Context, userID, targetID int64) (bool, error) {
	if m.isBlockedErr != nil {
		return false, m.isBlockedErr
	}
	return m.isBlocked, nil
}

type mockUserClient struct{}

func (m *mockUserClient) GetUserInfo(ctx context.Context, userID int64) (*userpb.UserInfo, error) {
	return &userpb.UserInfo{Id: userID, Username: "testuser", Avatar: "https://example.com/avatar.png"}, nil
}

func (m *mockUserClient) BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*userpb.UserInfo, error) {
	result := make(map[int64]*userpb.UserInfo)
	for _, id := range userIDs {
		result[id] = &userpb.UserInfo{Id: id, Username: "testuser", Avatar: "https://example.com/avatar.png"}
	}
	return result, nil
}
