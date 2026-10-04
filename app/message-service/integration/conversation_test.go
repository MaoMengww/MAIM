//go:build integration

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	serviceconfig "github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	convpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/gorm"
)

func cleanupConversation(t *testing.T, svcCtx *svc.ServiceContext, convID int64) {
	t.Helper()
	for _, table := range []string{"conv_bots", "conv_settings", "conv_read_seqs", "conv_members"} {
		require.NoError(t, svcCtx.DB.Exec("DELETE FROM msg."+table+" WHERE conv_id = ?", convID).Error)
	}
	require.NoError(t, svcCtx.ConversationRepo.DeleteConversation(context.Background(), convID))
}

func newConvSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c serviceconfig.Config
	config.SetLocalDefaults()
	conf.MustLoad("../etc/message.yaml", &c, conf.UseEnv())
	c.Telemetry.Endpoint = ""

	svcCtx := svc.NewServiceContext(c)
	t.Cleanup(func() {
		for _, producer := range []*kafka.Producer{
			svcCtx.MessageCreatedProducer, svcCtx.MessageRecalledProducer,
			svcCtx.MessageEditedProducer, svcCtx.MessageDeletedProducer,
			svcCtx.BotEventProducer, svcCtx.ReadUpdatedProducer,
		} {
			if producer != nil {
				assert.NoError(t, producer.Close())
			}
		}
		assert.NoError(t, svcCtx.Redis.Close())
		assert.NoError(t, svcCtx.DB.Close())
	})
	return svcCtx
}

// createTestUser inserts a minimal user record via GORM and returns the ID.
func createTestUser(t *testing.T, svcCtx *svc.ServiceContext) int64 {
	t.Helper()
	snowID, err := svcCtx.Snowflake.Generate()
	require.NoError(t, err)
	now := time.Now()
	err = svcCtx.DB.WithContext(t.Context()).Exec(
		`INSERT INTO "user".users (id, username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		snowID, "convusr_"+strconv.FormatInt(snowID%1000000, 36), "hash", now, now,
	).Error
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, svcCtx.DB.Exec(`DELETE FROM "user".users WHERE id = ?`, snowID).Error)
	})
	return snowID
}

func TestConversationPrivate(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

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
		cleanupConversation(t, svcCtx, convID)
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

	// Get members
	memLogic := conversationservice.NewGetMembersLogic(ctx, svcCtx)
	members, err := memLogic.GetMembers(&convpb.GetMembersReq{
		ConversationId: convID,
		UserId:         user1,
	})
	require.NoError(t, err)
	memberIDs := make([]int64, len(members.Members))
	for i, member := range members.Members {
		memberIDs[i] = member.UserId
	}
	assert.ElementsMatch(t, []int64{user1, user2}, memberIDs)
}

func TestConversationGroup(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

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
		cleanupConversation(t, svcCtx, convID)
	})

	// Verify owner role
	member, err := svcCtx.ConversationRepo.GetMember(ctx, convID, ownerID)
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
	getResp, err = getLogic.GetConversation(&convpb.GetConversationReq{ConversationId: convID, UserId: ownerID})
	require.NoError(t, err)
	assert.Equal(t, announcement, getResp.Conversation.Announcement)
}

func TestConversationMemberRole(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

	ownerID := createTestUser(t, svcCtx)
	memberID := createTestUser(t, svcCtx)
	name := "role-test-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	// Create group with owner and add member

	createLogic := conversationservice.NewCreateConversationLogic(ctx, svcCtx)
	resp, err := createLogic.CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		Name:      &name,
		MemberIds: []int64{memberID},
	})
	require.NoError(t, err)
	convID := resp.ConversationId

	t.Cleanup(func() {
		cleanupConversation(t, svcCtx, convID)
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
	member, err := svcCtx.ConversationRepo.GetMember(ctx, convID, memberID)
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
	member2, err := svcCtx.ConversationRepo.GetMember(ctx, convID, memberID)
	require.NoError(t, err)
	assert.True(t, member2.IsMuted)
}

func TestConversationReadStatus(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

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
		cleanupConversation(t, svcCtx, convID)
	})

	// A read position cannot advance beyond the conversation's persisted maximum.
	require.NoError(t, svcCtx.DB.Exec(`UPDATE msg.conversations SET max_seq = ? WHERE id = ?`, int64(100), convID).Error)
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
	assert.Equal(t, int32(1), readStatus.ReadCount)
	require.Len(t, readStatus.ReadUsers, 1)
	assert.Equal(t, ownerID, readStatus.ReadUsers[0].UserId)
	assert.Equal(t, int64(100), readStatus.ReadUsers[0].LastReadSeq)
}

func TestConversationDelete(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

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
	t.Cleanup(func() {
		cleanupConversation(t, svcCtx, convID)
	})

	// Delete conversation
	deleteLogic := conversationservice.NewDeleteConversationLogic(ctx, svcCtx)
	_, err = deleteLogic.DeleteConversation(&convpb.DeleteConversationReq{
		ConversationId: convID,
		UserId:         ownerID,
	})
	require.NoError(t, err)

	// Verify deleted
	_, err = svcCtx.ConversationRepo.GetConversation(ctx, convID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
