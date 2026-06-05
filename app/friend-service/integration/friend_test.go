//go:build integration

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/maomeng/aim/app/friend-service/internal/config"
	"github.com/maomeng/aim/app/friend-service/internal/logic"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	friendpb "github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
)

func newFriendSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	conf.MustLoad("../etc/friend.yaml", &c)
	c.Telemetry.Endpoint = ""

	return svc.NewServiceContext(c)
}

func userCtx(userID int64) context.Context {
	return context.WithValue(context.Background(), svc.CtxKeyUserID, userID)
}

func TestFriendRequestFlow(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	// Send friend request
	sendLogic := logic.NewSendRequestLogic(userCtx(user1), svcCtx)
	resp, err := sendLogic.SendRequest(&friendpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)
	require.Greater(t, resp.RequestId, int64(0))
	reqID := resp.RequestId

	t.Cleanup(func() {
		svcCtx.FriendRequestRepo.UpdateStatus(context.Background(), reqID, 4)
	})

	// List pending (recipient side)
	listPendingLogic := logic.NewListPendingRequestsLogic(userCtx(user2), svcCtx)
	pending, err := listPendingLogic.ListPendingRequests(&friendpb.ListRequestsReq{
		Pagination: &common.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(pending.Requests), 1)

	// Accept request
	acceptLogic := logic.NewAcceptRequestLogic(userCtx(user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&friendpb.AcceptRequestReq{
		RequestId: reqID,
	})
	require.NoError(t, err)

	// Verify friends
	listLogic := logic.NewListFriendsLogic(userCtx(user1), svcCtx)
	friends, err := listLogic.ListFriends(&friendpb.ListFriendsReq{
		Pagination: &common.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(friends.Friends), 1)
}

func TestFriendRequestReject(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	sendLogic := logic.NewSendRequestLogic(userCtx(user1), svcCtx)
	resp, err := sendLogic.SendRequest(&friendpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)

	// Reject
	rejectLogic := logic.NewRejectRequestLogic(userCtx(user2), svcCtx)
	_, err = rejectLogic.RejectRequest(&friendpb.RejectRequestReq{
		RequestId: resp.RequestId,
	})
	require.NoError(t, err)
}

func TestFriendRequestCancel(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	sendLogic := logic.NewSendRequestLogic(userCtx(user1), svcCtx)
	resp, err := sendLogic.SendRequest(&friendpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)

	// Cancel
	cancelLogic := logic.NewCancelRequestLogic(userCtx(user1), svcCtx)
	_, err = cancelLogic.CancelRequest(&friendpb.CancelRequestReq{
		RequestId: resp.RequestId,
	})
	require.NoError(t, err)
}

func TestFriendDelete(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	// Become friends
	sendLogic := logic.NewSendRequestLogic(userCtx(user1), svcCtx)
	resp, err := sendLogic.SendRequest(&friendpb.SendRequestReq{ToUserId: user2})
	require.NoError(t, err)

	acceptLogic := logic.NewAcceptRequestLogic(userCtx(user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&friendpb.AcceptRequestReq{RequestId: resp.RequestId})
	require.NoError(t, err)

	// Delete friend
	deleteLogic := logic.NewDeleteFriendLogic(userCtx(user1), svcCtx)
	_, err = deleteLogic.DeleteFriend(&friendpb.DeleteFriendReq{FriendId: user2})
	require.NoError(t, err)

	// Verify not friend
	isFriend, err := svcCtx.FriendRepo.IsFriend(context.Background(), user1, user2)
	require.NoError(t, err)
	assert.False(t, isFriend)
}

func TestFriendBlock(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	// Block user2
	blockLogic := logic.NewBlockUserLogic(userCtx(user1), svcCtx)
	_, err := blockLogic.BlockUser(&friendpb.BlockUserReq{BlockedUserId: user2})
	require.NoError(t, err)

	// Verify blocked
	isBlockedLogic := logic.NewIsBlockedLogic(userCtx(user1), svcCtx)
	blocked, err := isBlockedLogic.IsBlocked(&friendpb.IsBlockedReq{TargetUserId: user2})
	require.NoError(t, err)
	assert.True(t, blocked.IsBlocked)

	// Unblock
	unblockLogic := logic.NewUnblockUserLogic(userCtx(user1), svcCtx)
	_, err = unblockLogic.UnblockUser(&friendpb.UnblockUserReq{BlockedUserId: user2})
	require.NoError(t, err)

	// Verify not blocked
	blocked2, err := isBlockedLogic.IsBlocked(&friendpb.IsBlockedReq{TargetUserId: user2})
	require.NoError(t, err)
	assert.False(t, blocked2.IsBlocked)
}

func TestFriendGroup(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	groupName := "grp_" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	// Create group
	createGroupLogic := logic.NewCreateGroupLogic(userCtx(user1), svcCtx)
	grpResp, err := createGroupLogic.CreateGroup(&friendpb.CreateGroupReq{Name: groupName})
	require.NoError(t, err)
	require.Greater(t, grpResp.GroupId, int64(0))
	groupID := grpResp.GroupId

	// List groups
	listGroupsLogic := logic.NewListGroupsLogic(userCtx(user1), svcCtx)
	groups, err := listGroupsLogic.ListGroups(&common.Empty{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(groups.Groups), 1)

	// Rename group
	renameLogic := logic.NewRenameGroupLogic(userCtx(user1), svcCtx)
	_, err = renameLogic.RenameGroup(&friendpb.RenameGroupReq{
		GroupId: groupID,
		Name:    groupName + "_renamed",
	})
	require.NoError(t, err)

	// Delete group
	deleteGroupLogic := logic.NewDeleteGroupLogic(userCtx(user1), svcCtx)
	_, err = deleteGroupLogic.DeleteGroup(&friendpb.DeleteGroupReq{GroupId: groupID})
	require.NoError(t, err)
}

func TestFriendSetRemark(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := svcCtx.Snowflake.Generate()
	user2 := svcCtx.Snowflake.Generate()

	// Become friends
	sendLogic := logic.NewSendRequestLogic(userCtx(user1), svcCtx)
	resp, err := sendLogic.SendRequest(&friendpb.SendRequestReq{ToUserId: user2})
	require.NoError(t, err)

	acceptLogic := logic.NewAcceptRequestLogic(userCtx(user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&friendpb.AcceptRequestReq{RequestId: resp.RequestId})
	require.NoError(t, err)

	// Set remark
	remarkLogic := logic.NewSetRemarkLogic(userCtx(user1), svcCtx)
	_, err = remarkLogic.SetRemark(&friendpb.SetRemarkReq{
		FriendId: user2,
		Remark:   "my-friend",
	})
	require.NoError(t, err)
}
