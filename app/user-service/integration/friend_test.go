//go:build integration

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	logic "github.com/maomeng/aim/app/user-service/internal/logic/friend"
	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFriendSvcCtx(t *testing.T) *logic.Context {
	t.Helper()
	return newUserSvcCtx(t).FriendContext
}

func newFriendUser(t *testing.T, ctx *logic.Context) string {
	t.Helper()
	userID, err := identity.New()
	require.NoError(t, err)
	suffix := userID
	err = ctx.DB.WithContext(t.Context()).Create(&model.User{
		ID:       userID,
		Username: "friend_int_" + suffix,
		Phone:    "",
		Email:    suffix + "@test.com",
		Avatar:   "https://example.com/" + suffix + ".png",
	}).Error
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, ctx.DB.Where("from_user_id = ? OR to_user_id = ?", userID, userID).Delete(&model.FriendRequest{}).Error)
		assert.NoError(t, ctx.DB.Where("user_id = ? OR friend_id = ?", userID, userID).Delete(&model.Friend{}).Error)
		assert.NoError(t, ctx.DB.Where("user_id = ?", userID).Delete(&model.FriendGroup{}).Error)
		assert.NoError(t, ctx.DB.Where("user_id = ? OR blocked_user_id = ?", userID, userID).Delete(&model.UserBlock{}).Error)
		assert.NoError(t, ctx.DB.Where("id = ?", userID).Delete(&model.User{}).Error)
	})
	return userID
}

func userCtx(t *testing.T, userID string) context.Context {
	return context.WithValue(t.Context(), interceptor.ContextKeyUserID, userID)
}

func TestFriendRequestFlow(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	// Send friend request
	sendLogic := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx)
	resp, err := sendLogic.SendRequest(&userpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)
	require.NoError(t, identity.Validate(resp.RequestId))
	reqID := resp.RequestId

	// List pending (recipient side)
	listPendingLogic := logic.NewListPendingRequestsLogic(userCtx(t, user2), svcCtx)
	pending, err := listPendingLogic.ListPendingRequests(&userpb.ListPendingRequestsReq{
		Pagination: &common.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	require.Len(t, pending.Requests, 1)
	assert.Equal(t, reqID, pending.Requests[0].RequestId)
	assert.Equal(t, "friend_int_"+user1, pending.Requests[0].FromUsername)

	// Accept request
	acceptLogic := logic.NewAcceptRequestLogic(userCtx(t, user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&userpb.AcceptRequestReq{
		RequestId: reqID,
	})
	require.NoError(t, err)

	// Verify friends
	listLogic := logic.NewListFriendsLogic(userCtx(t, user1), svcCtx)
	friends, err := listLogic.ListFriends(&userpb.ListFriendsReq{
		Pagination: &common.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	assert.Equal(t, user2, friends.Friends[0].UserId)
	assert.Equal(t, "friend_int_"+user2, friends.Friends[0].Username)
}

func TestFriendRequestReject(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	sendLogic := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx)
	resp, err := sendLogic.SendRequest(&userpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)

	// Reject
	rejectLogic := logic.NewRejectRequestLogic(userCtx(t, user2), svcCtx)
	_, err = rejectLogic.RejectRequest(&userpb.RejectRequestReq{
		RequestId: resp.RequestId,
	})
	require.NoError(t, err)
	sent, err := logic.NewListSentRequestsLogic(userCtx(t, user1), svcCtx).ListSentRequests(&userpb.ListSentRequestsReq{})
	require.NoError(t, err)
	require.Len(t, sent.Requests, 1)
	assert.Equal(t, "rejected", sent.Requests[0].Status)
}

func TestFriendRequestCancel(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	sendLogic := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx)
	resp, err := sendLogic.SendRequest(&userpb.SendRequestReq{
		ToUserId: user2,
		Message:  "hello",
	})
	require.NoError(t, err)

	// Cancel
	cancelLogic := logic.NewCancelRequestLogic(userCtx(t, user1), svcCtx)
	_, err = cancelLogic.CancelRequest(&userpb.CancelRequestReq{
		RequestId: resp.RequestId,
	})
	require.NoError(t, err)
	sent, err := logic.NewListSentRequestsLogic(userCtx(t, user1), svcCtx).ListSentRequests(&userpb.ListSentRequestsReq{})
	require.NoError(t, err)
	require.Len(t, sent.Requests, 1)
	assert.Equal(t, "cancelled", sent.Requests[0].Status)
}

func TestFriendDelete(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	// Become friends
	sendLogic := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx)
	resp, err := sendLogic.SendRequest(&userpb.SendRequestReq{ToUserId: user2})
	require.NoError(t, err)

	acceptLogic := logic.NewAcceptRequestLogic(userCtx(t, user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&userpb.AcceptRequestReq{RequestId: resp.RequestId})
	require.NoError(t, err)

	// Delete friend
	deleteLogic := logic.NewDeleteFriendLogic(userCtx(t, user1), svcCtx)
	_, err = deleteLogic.DeleteFriend(&userpb.DeleteFriendReq{FriendId: user2})
	require.NoError(t, err)

	// Verify not friend
	isFriend, err := svcCtx.FriendRepo.IsFriend(t.Context(), user1, user2)
	require.NoError(t, err)
	assert.False(t, isFriend)
}

func TestFriendBlock(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	// Block user2
	blockLogic := logic.NewBlockUserLogic(userCtx(t, user1), svcCtx)
	_, err := blockLogic.BlockUser(&userpb.BlockUserReq{BlockedUserId: user2})
	require.NoError(t, err)

	// Verify blocked
	isBlockedLogic := logic.NewIsBlockedLogic(userCtx(t, user1), svcCtx)
	blocked, err := isBlockedLogic.IsBlocked(&userpb.IsBlockedReq{UserId: user1, TargetUserId: user2})
	require.NoError(t, err)
	assert.True(t, blocked.IsBlocked)

	// Unblock
	unblockLogic := logic.NewUnblockUserLogic(userCtx(t, user1), svcCtx)
	_, err = unblockLogic.UnblockUser(&userpb.UnblockUserReq{BlockedUserId: user2})
	require.NoError(t, err)

	// Verify not blocked
	blocked2, err := isBlockedLogic.IsBlocked(&userpb.IsBlockedReq{UserId: user1, TargetUserId: user2})
	require.NoError(t, err)
	assert.False(t, blocked2.IsBlocked)
}

func TestFriendGroup(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	groupName := "grp_" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	// Create group
	createGroupLogic := logic.NewCreateGroupLogic(userCtx(t, user1), svcCtx)
	grpResp, err := createGroupLogic.CreateGroup(&userpb.CreateGroupReq{Name: groupName})
	require.NoError(t, err)
	require.NoError(t, identity.Validate(grpResp.GroupId))
	groupID := grpResp.GroupId
	user2 := newFriendUser(t, svcCtx)
	request, err := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx).SendRequest(&userpb.SendRequestReq{ToUserId: user2})
	require.NoError(t, err)
	_, err = logic.NewAcceptRequestLogic(userCtx(t, user2), svcCtx).AcceptRequest(&userpb.AcceptRequestReq{RequestId: request.RequestId})
	require.NoError(t, err)
	setGroup := logic.NewSetGroupLogic(userCtx(t, user1), svcCtx)
	_, err = setGroup.SetGroup(&userpb.SetGroupReq{FriendId: user2, GroupId: &groupID})
	require.NoError(t, err)
	_, err = setGroup.SetGroup(&userpb.SetGroupReq{FriendId: user2})
	require.NoError(t, err)
	friends, err := logic.NewListFriendsLogic(userCtx(t, user1), svcCtx).ListFriends(&userpb.ListFriendsReq{})
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	require.NotNil(t, friends.Friends[0].GroupId)
	assert.Equal(t, groupID, *friends.Friends[0].GroupId)
	_, err = setGroup.SetGroup(&userpb.SetGroupReq{FriendId: user2, GroupId: &groupID, ClearGroupId: true})
	require.Error(t, err)
	_, err = setGroup.SetGroup(&userpb.SetGroupReq{FriendId: user2, ClearGroupId: true})
	require.NoError(t, err)
	friends, err = logic.NewListFriendsLogic(userCtx(t, user1), svcCtx).ListFriends(&userpb.ListFriendsReq{})
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	assert.Nil(t, friends.Friends[0].GroupId)
	_, err = setGroup.SetGroup(&userpb.SetGroupReq{FriendId: user2, GroupId: &groupID})
	require.NoError(t, err)

	// List groups
	listGroupsLogic := logic.NewListGroupsLogic(userCtx(t, user1), svcCtx)
	groups, err := listGroupsLogic.ListGroups(&userpb.ListGroupsReq{})
	require.NoError(t, err)
	require.Len(t, groups.Groups, 1)
	assert.Equal(t, groupID, groups.Groups[0].Id)
	assert.Equal(t, groupName, groups.Groups[0].Name)

	// Rename group
	renameLogic := logic.NewRenameGroupLogic(userCtx(t, user1), svcCtx)
	_, err = renameLogic.RenameGroup(&userpb.RenameGroupReq{
		GroupId: groupID,
		Name:    groupName + "_renamed",
	})
	require.NoError(t, err)
	groups, err = listGroupsLogic.ListGroups(&userpb.ListGroupsReq{})
	require.NoError(t, err)
	require.Len(t, groups.Groups, 1)
	assert.Equal(t, groupName+"_renamed", groups.Groups[0].Name)

	// Delete group
	deleteGroupLogic := logic.NewDeleteGroupLogic(userCtx(t, user1), svcCtx)
	_, err = deleteGroupLogic.DeleteGroup(&userpb.DeleteGroupReq{GroupId: groupID})
	require.NoError(t, err)
	groups, err = listGroupsLogic.ListGroups(&userpb.ListGroupsReq{})
	require.NoError(t, err)
	assert.Empty(t, groups.Groups)
	friends, err = logic.NewListFriendsLogic(userCtx(t, user1), svcCtx).ListFriends(&userpb.ListFriendsReq{})
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	assert.Nil(t, friends.Friends[0].GroupId)
}

func TestFriendSetRemark(t *testing.T) {
	svcCtx := newFriendSvcCtx(t)

	user1 := newFriendUser(t, svcCtx)
	user2 := newFriendUser(t, svcCtx)

	// Become friends
	sendLogic := logic.NewSendRequestLogic(userCtx(t, user1), svcCtx)
	resp, err := sendLogic.SendRequest(&userpb.SendRequestReq{ToUserId: user2})
	require.NoError(t, err)

	acceptLogic := logic.NewAcceptRequestLogic(userCtx(t, user2), svcCtx)
	_, err = acceptLogic.AcceptRequest(&userpb.AcceptRequestReq{RequestId: resp.RequestId})
	require.NoError(t, err)

	// Set remark
	remarkLogic := logic.NewSetRemarkLogic(userCtx(t, user1), svcCtx)
	_, err = remarkLogic.SetRemark(&userpb.SetRemarkReq{
		FriendId: user2,
		Remark:   "my-friend",
	})
	require.NoError(t, err)
	friends, err := logic.NewListFriendsLogic(userCtx(t, user1), svcCtx).ListFriends(&userpb.ListFriendsReq{})
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	assert.Equal(t, "my-friend", friends.Friends[0].Remark)
}
