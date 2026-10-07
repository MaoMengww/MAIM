//go:build integration

package integration

import (
	"strconv"
	"testing"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/consumer"
	"github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	convpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// Delivery resolves a conversation's human members as recipients; a member
// stored under any other type is silently undeliverable.
func TestFanoutRecipientsIncludePrivateMembers(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

	creator := createTestUser(t, svcCtx)
	peer := createTestUser(t, svcCtx)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("user-id", creator))

	resp, err := conversationservice.NewCreateConversationLogic(ctx, svcCtx).CreateConversation(&convpb.CreateConversationReq{
		Type:       convpb.ConversationType_CONVERSATION_TYPE_PRIVATE,
		CreatorId:  creator,
		PeerUserId: &peer,
	})
	require.NoError(t, err)
	convID := resp.ConversationId
	t.Cleanup(func() { cleanupConversation(t, svcCtx, convID) })

	recipients, err := consumer.NewFanout(svcCtx.ConversationRepo, nil).UserIDs(ctx, convID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{creator, peer}, recipients)
}

// Group members joined at creation and later additions are both deliverable.
func TestFanoutRecipientsIncludeGroupMembers(t *testing.T) {
	svcCtx := newConvSvcCtx(t)
	ctx := t.Context()

	owner := createTestUser(t, svcCtx)
	founding := createTestUser(t, svcCtx)
	added := createTestUser(t, svcCtx)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("user-id", owner))
	name := "fanout-group-" + strconv.FormatInt(time.Now().UnixMilli(), 36)

	resp, err := conversationservice.NewCreateConversationLogic(ctx, svcCtx).CreateConversation(&convpb.CreateConversationReq{
		Type:      convpb.ConversationType_CONVERSATION_TYPE_GROUP,
		CreatorId: owner,
		Name:      &name,
		MemberIds: []string{founding},
	})
	require.NoError(t, err)
	convID := resp.ConversationId
	t.Cleanup(func() { cleanupConversation(t, svcCtx, convID) })

	addResp, err := conversationservice.NewAddMembersLogic(ctx, svcCtx).AddMembers(&convpb.AddMembersReq{
		ConversationId: convID,
		OperatorId:     owner,
		UserIds:        []string{added},
	})
	require.NoError(t, err)
	require.Equal(t, []string{added}, addResp.AddedUserIds)

	recipients, err := consumer.NewFanout(svcCtx.ConversationRepo, nil).UserIDs(ctx, convID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{owner, founding, added}, recipients)
}
