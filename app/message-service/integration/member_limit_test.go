//go:build integration

package integration

import (
	"github.com/google/uuid"
	"sync"
	"testing"

	"github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	messageservicelogic "github.com/maomeng/aim/app/message-service/internal/logic/messageservice"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	convpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// sendText drives the same call the gateway makes, including the caller identity
// it carries in gRPC metadata.
func sendText(t *testing.T, svcCtx *svc.ServiceContext, convID, senderID string, text string) {
	t.Helper()
	ctx := metadata.NewIncomingContext(t.Context(),
		metadata.Pairs("user-id", senderID, "device-id", "member-limit-test"))
	resp, err := messageservicelogic.NewSendMessageLogic(ctx, svcCtx).SendMessage(&convpb.SendMessageReq{
		ConversationId: convID,
		FromUserId:     senderID,
		ClientMsgId:    uuid.NewString(),
		Type:           convpb.MessageType_MESSAGE_TYPE_TEXT,
		Content:        &convpb.SendMessageReq_Text{Text: &convpb.TextContent{Text: text}},
	})
	require.NoError(t, err)
	require.Positive(t, resp.Seq)
}

func assertConversationMembers(t *testing.T, svcCtx *svc.ServiceContext, convID, ownerID string, want []string) {
	t.Helper()
	members, err := conversationservice.NewGetMembersLogic(conversationContext(t, ownerID), svcCtx).GetMembers(&convpb.GetMembersReq{
		ConversationId: convID,
		UserId:         ownerID,
	})
	require.NoError(t, err)
	ids := make([]string, len(members.Members))
	for i, member := range members.Members {
		ids[i] = member.GetUserId()
	}
	assert.ElementsMatch(t, want, ids)
	assert.Equal(t, int64(len(want)), members.Pagination.Total)

	conv, err := conversationservice.NewGetConversationLogic(conversationContext(t, ownerID), svcCtx).GetConversation(&convpb.GetConversationReq{
		ConversationId: convID,
		UserId:         ownerID,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(len(want)), conv.Conversation.MemberCount)
}

func TestConversationMemberLimitAtomicBatch(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	svcCtx.Config.Conv.MaxMemberCount = 3

	ownerID := createTestUser(t, svcCtx)
	ctx := conversationContext(t, ownerID)
	existingID := createTestUser(t, svcCtx)
	firstID := createTestUser(t, svcCtx)
	secondID := createTestUser(t, svcCtx)
	created, err := conversationservice.NewCreateConversationLogic(ctx, svcCtx).CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		MemberIds: []string{existingID},
	})
	require.NoError(t, err)
	convID := created.ConversationId
	t.Cleanup(func() { cleanupConversation(t, svcCtx, convID) })
	add := conversationservice.NewAddMembersLogic(ctx, svcCtx)

	// A batch needing two new slots must not consume the one remaining slot.
	_, err = add.AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserIds:        []string{existingID, firstID, firstID, secondID},
	})
	require.ErrorIs(t, err, conversationservice.ErrConvMaxMembers)
	assertConversationMembers(t, svcCtx, convID, ownerID, []string{ownerID, existingID})

	// Existing members and repeated IDs do not consume additional slots.
	added, err := add.AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserIds:        []string{existingID, firstID, firstID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{firstID}, added.AddedUserIds)
	assert.Equal(t, []string{existingID, firstID}, added.FailedUserIds)
	assertConversationMembers(t, svcCtx, convID, ownerID, []string{ownerID, existingID, firstID})

	duplicates, err := add.AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserIds:        []string{ownerID, existingID, firstID},
	})
	require.NoError(t, err)
	assert.Empty(t, duplicates.AddedUserIds)
	assert.Equal(t, []string{ownerID, existingID, firstID}, duplicates.FailedUserIds)

	_, err = add.AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserIds:        []string{secondID},
	})
	require.ErrorIs(t, err, conversationservice.ErrConvMaxMembers)
	assertConversationMembers(t, svcCtx, convID, ownerID, []string{ownerID, existingID, firstID})

	// Leaving releases capacity, and the persisted count follows the replacement.
	_, err = conversationservice.NewRemoveMembersLogic(conversationContext(t, firstID), svcCtx).RemoveMembers(&convpb.RemoveMembersReq{
		ConversationId: convID,
		OperatorId:     firstID,
		UserIds:        []string{firstID},
	})
	require.NoError(t, err)
	added, err = add.AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     ownerID,
		UserIds:        []string{secondID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{secondID}, added.AddedUserIds)
	assertConversationMembers(t, svcCtx, convID, ownerID, []string{ownerID, existingID, secondID})

	// A rejected addition must leave the rest of the conversation working.
	sendText(t, svcCtx, convID, ownerID, "still usable after rejection")
}

func TestConversationMemberLimitConcurrentAdds(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	svcCtx.Config.Conv.MaxMemberCount = 3

	ownerID := createTestUser(t, svcCtx)
	ctx := conversationContext(t, ownerID)
	existingID := createTestUser(t, svcCtx)
	firstID := createTestUser(t, svcCtx)
	secondID := createTestUser(t, svcCtx)
	created, err := conversationservice.NewCreateConversationLogic(ctx, svcCtx).CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: ownerID,
		MemberIds: []string{existingID},
	})
	require.NoError(t, err)
	convID := created.ConversationId
	t.Cleanup(func() { cleanupConversation(t, svcCtx, convID) })

	type result struct {
		userID string
		resp   *convpb.AddMembersResp
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var requests sync.WaitGroup
	for _, uid := range []string{firstID, secondID} {
		requests.Go(func() {
			<-start
			resp, err := conversationservice.NewAddMembersLogic(ctx, svcCtx).AddMembers(&convpb.AddMembersReq{
				ConversationId: convID,
				OperatorId:     ownerID,
				UserIds:        []string{uid},
			})
			results <- result{userID: uid, resp: resp, err: err}
		})
	}
	close(start)
	requests.Wait()
	close(results)

	var winnerID string
	succeeded, rejected := 0, 0
	for res := range results {
		if res.err != nil {
			require.ErrorIs(t, res.err, conversationservice.ErrConvMaxMembers)
			rejected++
			continue
		}
		require.NotNil(t, res.resp)
		assert.Equal(t, []string{res.userID}, res.resp.AddedUserIds)
		assert.Empty(t, res.resp.FailedUserIds)
		winnerID = res.userID
		succeeded++
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, rejected)
	assertConversationMembers(t, svcCtx, convID, ownerID, []string{ownerID, existingID, winnerID})
}
