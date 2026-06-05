//go:build integration

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/config"
	"github.com/maomeng/aim/app/conversation-service/internal/logic/conversationservice"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	convpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
)

func newConvSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	conf.MustLoad("../etc/conversation.yaml", &c)
	c.Telemetry.Endpoint = ""

	return svc.NewServiceContext(c)
}

// createTestUser inserts a minimal user record via GORM and returns the ID.
func createTestUser(t *testing.T, svcCtx *svc.ServiceContext) int64 {
	t.Helper()
	snowID := svcCtx.Snowflake.Generate()
	now := time.Now()
	err := svcCtx.DB.WithContext(context.Background()).Exec(
		`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		snowID, "convusr_"+strconv.FormatInt(snowID%1000000, 36), "hash", now, now,
	).Error
	require.NoError(t, err)
	return snowID
}

func TestConversationPrivate(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := context.Background()

	user1 := createTestUser(t, svcCtx)
	user2 := createTestUser(t, svcCtx)

	logic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := logic.CreateConversation(&convpb.CreateConversationReq{
		Type:       convpb.ConversationType_CONVERSATION_TYPE_PRIVATE,
		CreatorId:  user1,
		PeerUserId: &user2,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Greater(t, resp.ConversationId, int64(0))
	convID := resp.ConversationId

	t.Cleanup(func() {
		svcCtx.Repo.DeleteConversation(ctx, convID)
	})

	// Get conversation
	getLogic := conversationservice.NewGetConversationLogic(ctx, svcCtx)
	getResp, err := getLogic.GetConversation(&convpb.GetConversationReq{
		ConversationId: convID,
		UserId:         user1,
	})
	require.NoError(t, err)
	assert.Equal(t, convID, getResp.Conversation.Id)
	assert.Equal(t, convpb.ConversationType_CONVERSATION_TYPE_PRIVATE, getResp.Conversation.Type)

	// IsMember
	isMemLogic := conversationservice.NewIsMemberLogic(ctx, svcCtx)
	isMem, err := isMemLogic.IsMember(&convpb.IsMemberReq{
		ConversationId: convID,
		UserId:         user1,
	})
	require.NoError(t, err)
	assert.True(t, isMem.IsMember)

	// Get members
	memLogic := conversationservice.NewGetMembersLogic(ctx, svcCtx)
	members, err := memLogic.GetMembers(&convpb.GetMembersReq{
		ConversationId: convID,
		UserId:         user1,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(members.Members), 1)
}

func TestConversationGroup(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := context.Background()

	ownerID := createTestUser(t, svcCtx)
	name := "test-group-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	logic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := logic.CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		Name:      &name,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	convID := resp.ConversationId

	t.Cleanup(func() {
		svcCtx.Repo.DeleteConversation(ctx, convID)
	})

	// Verify owner role
	member, err := svcCtx.Repo.GetMember(ctx, convID, ownerID)
	require.NoError(t, err)
	assert.Equal(t, int32(convpb.MemberRole_MEMBER_ROLE_OWNER), member.Role)

	// Update conversation name
	updateName := "updated-group-" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	updateLogic := conversationservice.NewUpdateConversationLogic(ctx, svcCtx)
	_, err = updateLogic.UpdateConversation(&convpb.UpdateConversationReq{
		ConversationId: convID,
		UserId:         ownerID,
		Name:           &updateName,
	})
	require.NoError(t, err)

	// Verify update
	getLogic := conversationservice.NewGetConversationLogic(ctx, svcCtx)
	getResp, err := getLogic.GetConversation(&convpb.GetConversationReq{
		ConversationId: convID,
		UserId:         ownerID,
	})
	require.NoError(t, err)
	assert.Equal(t, updateName, getResp.Conversation.Name)

	// Set announcement
	announcement := "This is a test announcement"
	annLogic := conversationservice.NewSetAnnouncementLogic(ctx, svcCtx)
	_, err = annLogic.SetAnnouncement(&convpb.SetAnnouncementReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		Content:        announcement,
	})
	require.NoError(t, err)
}

func TestConversationMemberRole(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := context.Background()

	ownerID := createTestUser(t, svcCtx)
	memberID := createTestUser(t, svcCtx)
	name := "role-test-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	// Create group with owner and add member

	createLogic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := createLogic.CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		Name:      &name,
	GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error)
		MemberIds: []int64{memberID},
	})
	require.NoError(t, err)
	convID := resp.ConversationId

	t.Cleanup(func() {
		svcCtx.Repo.DeleteConversation(ctx, convID)
	})

	// Promote member to admin
	updateMemberLogic := conversationservice.NewUpdateMemberLogic(ctx, svcCtx)
	_, err = updateMemberLogic.UpdateMember(&convpb.UpdateMemberReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserId:         memberID,
		Role:           convpb.MemberRole_MEMBER_ROLE_ADMIN.Enum(),
	})
	require.NoError(t, err)

	// Verify role
	member, err := svcCtx.Repo.GetMember(ctx, convID, memberID)
	require.NoError(t, err)
	assert.Equal(t, int32(convpb.MemberRole_MEMBER_ROLE_ADMIN), member.Role)

	// Mute member
	muteMemLogic := conversationservice.NewMuteMemberLogic(ctx, svcCtx)
	_, err = muteMemLogic.MuteMember(&convpb.MuteMemberReq{
		ConversationId:  convID,
		UserId:          memberID,
		OperatorId:      ownerID,
		DurationSeconds: 3600,
	})
	require.NoError(t, err)

	// Verify muted
	member2, err := svcCtx.Repo.GetMember(ctx, convID, memberID)
	require.NoError(t, err)
	assert.True(t, member2.IsMuted)
}

func TestConversationReadStatus(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := context.Background()

	ownerID := createTestUser(t, svcCtx)
	name := "read-test-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	createLogic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := createLogic.CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		Name:      &name,
	})
	require.NoError(t, err)
	convID := resp.ConversationId

	t.Cleanup(func() {
		svcCtx.Repo.DeleteConversation(ctx, convID)
	})

	// Mark as read
	readLogic := conversationservice.NewMarkAsReadLogic(ctx, svcCtx)
	_, err = readLogic.MarkAsRead(&convpb.MarkAsReadReq{
		ConversationId: convID,
		UserId:         ownerID,
		Seq:            100,
	})
	require.NoError(t, err)

	// Get read status
	getReadLogic := conversationservice.NewGetReadStatusLogic(ctx, svcCtx)
	readStatus, err := getReadLogic.GetReadStatus(&convpb.GetReadStatusReq{
		ConversationId: convID,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, readStatus.ReadCount, int32(1))
}

func TestConversationDelete(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := context.Background()

	ownerID := createTestUser(t, svcCtx)
	name := "del-test-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	createLogic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := createLogic.CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		Name:      &name,
	})
	require.NoError(t, err)
	convID := resp.ConversationId

	// Delete conversation
	deleteLogic := conversationservice.NewDeleteConversationLogic(ctx, svcCtx)
	_, err = deleteLogic.DeleteConversation(&convpb.DeleteConversationReq{
		ConversationId: convID,
		UserId:         ownerID,
	})
	require.NoError(t, err)

	// Verify deleted
	_, err = svcCtx.Repo.GetConversation(ctx, convID)
	assert.Error(t, err)
}
