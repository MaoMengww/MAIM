package conversationservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========== Mock Repo ==========

type mockRepo struct {
	createConvErr              error
	getBot                     *model.Bot
	getBotErr                  error
	addedBot                   *model.ConvBot
	addBotErr                  error
	addedMember                *model.ConversationMember
	addBotWithMemberErr        error
	removedBotConvID           int64
	removedBotID               int64
	removeBotErr               error
	removedBotWithMemberConvID int64
	removedBotWithMemberBotID  int64
	removeBotWithMemberErr     error
	removedBotMemberConvID     int64
	removedBotMemberBotID      int64
	removeBotMemberErr         error
	updateBotErr               error
	memberCountDeltas          []int
	getConv                    *model.Conversation
	getConvErr                 error
	listConvs                  []model.Conversation
	listPinnedConvs            []model.Conversation
	updateConvErr              error
	updateAnnounceErr          error
	updateMutedAllErr          error
	updateOwnerErr             error
	deleteConvErr              error
	addMemberErr               error
	addMembersBatchErr         error
	removeMemberErr            error
	getMember                  *model.ConversationMember
	getMemberFn                func(convID, userID int64) (*model.ConversationMember, error)
	getMemberErr               error
	getMembers                 []model.ConversationMember
	countMembers               int64
	countMembersErr            error
	updateMemberErr            error
	updateMemberRoleErr        error
	isMember                   bool
	isMemberErr                error
	muteMemberErr              error
	unmuteMemberErr            error
	upsertReadSeqErr           error
	readSeqs                   []model.ConvReadSeq
	readSeqsErr                error
	getReadSeq                 *model.ConvReadSeq
	getReadSeqErr              error
	getSettings                *model.ConvSettings
	getSettingsErr             error
	upsertSettingsErr          error
}

func (m *mockRepo) CreateConversation(ctx context.Context, conv *model.Conversation) error {
	return m.createConvErr
}
func (m *mockRepo) FindPrivateConv(ctx context.Context, userID1, userID2 int64) (*model.Conversation, error) {
	return nil, errors.New("not found")
}

func (m *mockRepo) GetConversation(ctx context.Context, id int64) (*model.Conversation, error) {
	return m.getConv, m.getConvErr
}
func (m *mockRepo) ListConversationsByUser(ctx context.Context, userID int64, cursor int64, limit int, typ *int32) ([]model.Conversation, error) {
	return m.listConvs, nil
}
func (m *mockRepo) ListConversationsByUserPinned(ctx context.Context, userID int64, pinned bool) ([]model.Conversation, error) {
	return m.listPinnedConvs, nil
}
func (m *mockRepo) UpdateConversation(ctx context.Context, conv *model.Conversation) error {
	return m.updateConvErr
}
func (m *mockRepo) UpdateConversationAnnouncement(ctx context.Context, id int64, announcement string) error {
	return m.updateAnnounceErr
}
func (m *mockRepo) UpdateConversationMutedAll(ctx context.Context, id int64, mutedAll bool) error {
	return m.updateMutedAllErr
}
func (m *mockRepo) UpdateConversationOwner(ctx context.Context, id int64, newOwnerID int64) error {
	return m.updateOwnerErr
}
func (m *mockRepo) DeleteConversation(ctx context.Context, id int64) error {
	return m.deleteConvErr
}
func (m *mockRepo) UpdateConversationLastMessage(ctx context.Context, id int64, msgID int64, preview string, seq int64) error {
	return nil
}
func (m *mockRepo) IncrementMemberCount(ctx context.Context, id int64, delta int) error {
	return nil
}
func (m *mockRepo) AddMember(ctx context.Context, member *model.ConversationMember) error {
	return m.addMemberErr
}
func (m *mockRepo) AddMembersBatch(ctx context.Context, members []model.ConversationMember) error {
	return m.addMembersBatchErr
}
func (m *mockRepo) RemoveMember(ctx context.Context, convID, userID int64) error {
	return m.removeMemberErr
}
func (m *mockRepo) GetMember(ctx context.Context, convID, userID int64) (*model.ConversationMember, error) {
	if m.getMemberFn != nil {
		return m.getMemberFn(convID, userID)
	}
	return m.getMember, m.getMemberErr
}
func (m *mockRepo) GetMembers(ctx context.Context, convID int64, offset, limit int) ([]model.ConversationMember, error) {
	members := m.getMembers
	if m.addedMember != nil {
		members = append(members, *m.addedMember)
	}
	return members, nil
}
func (m *mockRepo) CountMembers(ctx context.Context, convID int64) (int64, error) {
	return m.countMembers, m.countMembersErr
}
func (m *mockRepo) UpdateMember(ctx context.Context, mem *model.ConversationMember) error {
	return m.updateMemberErr
}
func (m *mockRepo) UpdateMemberRole(ctx context.Context, convID, userID int64, role int32) error {
	return m.updateMemberRoleErr
}
func (m *mockRepo) IsMember(ctx context.Context, convID, userID int64) (bool, error) {
	return m.isMember, m.isMemberErr
}
func (m *mockRepo) MuteMember(ctx context.Context, convID, userID int64, muteUntil int64) error {
	return m.muteMemberErr
}
func (m *mockRepo) UnmuteMember(ctx context.Context, convID, userID int64) error {
	return m.unmuteMemberErr
}
func (m *mockRepo) UpsertReadSeq(ctx context.Context, convID, userID int64, seq int64, id int64) error {
	return m.upsertReadSeqErr
}
func (m *mockRepo) GetReadSeq(ctx context.Context, convID, userID int64) (*model.ConvReadSeq, error) {
	return m.getReadSeq, m.getReadSeqErr
}
func (m *mockRepo) GetReadSeqs(ctx context.Context, convID int64) ([]model.ConvReadSeq, error) {
	return m.readSeqs, m.readSeqsErr
}
func (m *mockRepo) GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error) {
	return nil, nil
}
func (m *mockRepo) GetSettings(ctx context.Context, convID, userID int64) (*model.ConvSettings, error) {
	return m.getSettings, m.getSettingsErr
}
func (m *mockRepo) UpsertSettings(ctx context.Context, s *model.ConvSettings) error {
	return m.upsertSettingsErr
}

func newTestSvcCtx(repo *mockRepo) *svc.ServiceContext {
	sf, _ := snowflake.NewNode(1)
	return &svc.ServiceContext{
		Repo:      repo,
		Snowflake: sf,
	}
}

// ========== IsMember Tests ==========

func TestIsMember_True(t *testing.T) {
	repo := &mockRepo{isMember: true}
	svcCtx := newTestSvcCtx(repo)

	logic := NewIsMemberLogic(context.Background(), svcCtx)
	resp, err := logic.IsMember(&conversation.IsMemberReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.True(t, resp.IsMember)
}

func TestIsMember_False(t *testing.T) {
	repo := &mockRepo{isMember: false}
	svcCtx := newTestSvcCtx(repo)

	logic := NewIsMemberLogic(context.Background(), svcCtx)
	resp, err := logic.IsMember(&conversation.IsMemberReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.False(t, resp.IsMember)
}

func TestIsMember_Error(t *testing.T) {
	repo := &mockRepo{isMemberErr: errors.New("db error")}
	svcCtx := newTestSvcCtx(repo)

	logic := NewIsMemberLogic(context.Background(), svcCtx)
	resp, err := logic.IsMember(&conversation.IsMemberReq{
		ConversationId: 100,
		UserId:         10,
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// ========== GetMuteStatus Tests ==========

func TestGetMuteStatus_Muted(t *testing.T) {
	repo := &mockRepo{
		getMember: &model.ConversationMember{IsMuted: true, MuteUntil: 999},
		getConv:   &model.Conversation{IsMutedAll: false},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetMuteStatusLogic(context.Background(), svcCtx)
	resp, err := logic.GetMuteStatus(&conversation.GetMuteStatusReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.True(t, resp.IsMuted)
	assert.False(t, resp.IsMutedAll)
	assert.Equal(t, int64(999), resp.MuteUntil)
}

func TestGetMuteStatus_MutedAll(t *testing.T) {
	repo := &mockRepo{
		getMember: &model.ConversationMember{IsMuted: false},
		getConv:   &model.Conversation{IsMutedAll: true},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetMuteStatusLogic(context.Background(), svcCtx)
	resp, err := logic.GetMuteStatus(&conversation.GetMuteStatusReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.True(t, resp.IsMutedAll)
	assert.False(t, resp.IsMuted)
}

func TestGetMuteStatus_MemberNotFound(t *testing.T) {
	repo := &mockRepo{getMemberErr: errors.New("member not found")}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetMuteStatusLogic(context.Background(), svcCtx)
	resp, err := logic.GetMuteStatus(&conversation.GetMuteStatusReq{
		ConversationId: 100,
		UserId:         10,
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// ========== CreateConversation Tests ==========

func TestCreateConversation_MissingCreator(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewCreateConversationLogic(context.Background(), svcCtx)
	resp, err := logic.CreateConversation(&conversation.CreateConversationReq{
		CreatorId: 0,
		Type:      conversation.ConversationType_CONVERSATION_TYPE_GROUP,
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creator_id is required")
	assert.Nil(t, resp)
}

func TestCreateConversation_Success(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewCreateConversationLogic(context.Background(), svcCtx)
	resp, err := logic.CreateConversation(&conversation.CreateConversationReq{
		CreatorId: 10,
		Name:      strPtr("test group"),
		Type:      conversation.ConversationType_CONVERSATION_TYPE_GROUP,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotZero(t, resp.ConversationId)
	assert.NotNil(t, resp.Conversation)
}

func TestCreateConversation_RepeatedCreatorSkipsDup(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewCreateConversationLogic(context.Background(), svcCtx)
	resp, err := logic.CreateConversation(&conversation.CreateConversationReq{
		CreatorId: 10,
		MemberIds: []int64{10, 20, 30},
		Type:      conversation.ConversationType_CONVERSATION_TYPE_GROUP,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestCreateConversation_ReturnsBotPeerInfo(t *testing.T) {
	peerUserID := int64(333140609550278657)
	repo := &mockRepo{
		getBot: &model.Bot{
			ID:     333140609550278656,
			Name:   "智能问答助手",
			Avatar: "https://example.com/bot.png",
		},
		// getMembers only includes the creator; the bot peer member is added
		// dynamically via AddBotWithMember during conversation creation.
		getMembers: []model.ConversationMember{
			{UserID: 10},
		},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewCreateConversationLogic(context.Background(), svcCtx)
	resp, err := logic.CreateConversation(&conversation.CreateConversationReq{
		CreatorId:  10,
		Type:       conversation.ConversationType_CONVERSATION_TYPE_PRIVATE,
		PeerUserId: &peerUserID,
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Conversation)
	assert.Equal(t, "智能问答助手", resp.Conversation.Name)
	assert.Equal(t, "https://example.com/bot.png", resp.Conversation.Avatar)
}

// ========== DeleteConversation Tests ==========

func TestDeleteConversation_NotMember(t *testing.T) {
	repo := &mockRepo{getMemberErr: errors.New("not found")}
	svcCtx := newTestSvcCtx(repo)

	logic := NewDeleteConversationLogic(context.Background(), svcCtx)
	resp, err := logic.DeleteConversation(&conversation.DeleteConversationReq{
		ConversationId: 100,
		UserId:         10,
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestDeleteConversation_Success(t *testing.T) {
	repo := &mockRepo{getMember: &model.ConversationMember{UserID: 10, Role: 1}}
	svcCtx := newTestSvcCtx(repo)

	logic := NewDeleteConversationLogic(context.Background(), svcCtx)
	resp, err := logic.DeleteConversation(&conversation.DeleteConversationReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(0), resp.Code)
}

func TestDeleteConversation_IsMemberError(t *testing.T) {
	repo := &mockRepo{getMemberErr: errors.New("db error")}
	svcCtx := newTestSvcCtx(repo)

	logic := NewDeleteConversationLogic(context.Background(), svcCtx)
	resp, err := logic.DeleteConversation(&conversation.DeleteConversationReq{
		ConversationId: 100,
		UserId:         10,
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// ========== AddMembers Tests ==========

func TestAddMembers_SkipExisting(t *testing.T) {
	repo := &mockRepo{
		isMember:  true,
		getMember: &model.ConversationMember{Role: 1},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewAddMembersLogic(context.Background(), svcCtx)
	resp, err := logic.AddMembers(&conversation.AddMembersReq{
		ConversationId: 100,
		OperatorId:     10,
		UserIds:        []int64{20, 30},
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.AddedUserIds)
	assert.Len(t, resp.FailedUserIds, 2)
}

func TestAddMembers_AllNew(t *testing.T) {
	repo := &mockRepo{
		isMember:  false,
		getMember: &model.ConversationMember{Role: 1},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewAddMembersLogic(context.Background(), svcCtx)
	resp, err := logic.AddMembers(&conversation.AddMembersReq{
		ConversationId: 100,
		OperatorId:     10,
		UserIds:        []int64{20, 30},
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.AddedUserIds, 2)
	assert.Empty(t, resp.FailedUserIds)
}

// ========== RemoveMembers Tests ==========

func TestRemoveMembers_Success(t *testing.T) {
	repo := &mockRepo{
		getMemberFn: func(convID, userID int64) (*model.ConversationMember, error) {
			if userID == 10 {
				return &model.ConversationMember{UserID: 10, Role: 2}, nil
			}
			return &model.ConversationMember{UserID: userID, Role: 3}, nil
		},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewRemoveMembersLogic(context.Background(), svcCtx)
	resp, err := logic.RemoveMembers(&conversation.RemoveMembersReq{
		ConversationId: 100,
		OperatorId:     10,
		UserIds:        []int64{20, 30},
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== GetMembers Tests ==========

func TestGetMembers_Success(t *testing.T) {
	repo := &mockRepo{
		getMembers: []model.ConversationMember{
			{UserID: 10, Role: 1},
			{UserID: 20, Role: 3},
		},
		countMembers: 2,
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetMembersLogic(context.Background(), svcCtx)
	resp, err := logic.GetMembers(&conversation.GetMembersReq{
		ConversationId: 100,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Members, 2)
	assert.Equal(t, int64(2), resp.Pagination.Total)
}

// ========== MuteAll / UnmuteAll Tests ==========

func TestMuteAll_Success(t *testing.T) {
	repo := &mockRepo{getMember: &model.ConversationMember{UserID: 10, Role: 2}}
	svcCtx := newTestSvcCtx(repo)

	logic := NewMuteAllLogic(context.Background(), svcCtx)
	resp, err := logic.MuteAll(&conversation.MuteAllReq{ConversationId: 100, OperatorId: 10})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestUnmuteAll_Success(t *testing.T) {
	repo := &mockRepo{getMember: &model.ConversationMember{UserID: 10, Role: 2}}
	svcCtx := newTestSvcCtx(repo)

	logic := NewUnmuteAllLogic(context.Background(), svcCtx)
	resp, err := logic.UnmuteAll(&conversation.UnmuteAllReq{ConversationId: 100, OperatorId: 10})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== MuteMember / UnmuteMember Tests ==========

func TestMuteMember_Success(t *testing.T) {
	repo := &mockRepo{
		getMemberFn: func(convID, userID int64) (*model.ConversationMember, error) {
			if userID == 10 {
				return &model.ConversationMember{UserID: 10, Role: 2}, nil
			}
			return &model.ConversationMember{UserID: 20, Role: 3}, nil
		},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewMuteMemberLogic(context.Background(), svcCtx)
	resp, err := logic.MuteMember(&conversation.MuteMemberReq{
		ConversationId:  100,
		OperatorId:      10,
		UserId:          20,
		DurationSeconds: 3600,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestUnmuteMember_Success(t *testing.T) {
	repo := &mockRepo{
		getMemberFn: func(convID, userID int64) (*model.ConversationMember, error) {
			if userID == 10 {
				return &model.ConversationMember{UserID: 10, Role: 2}, nil
			}
			return &model.ConversationMember{UserID: 20, Role: 3}, nil
		},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewUnmuteMemberLogic(context.Background(), svcCtx)
	resp, err := logic.UnmuteMember(&conversation.UnmuteMemberReq{
		ConversationId: 100,
		OperatorId:     10,
		UserId:         20,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== MarkAsRead Tests ==========

func TestMarkAsRead_Success(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewMarkAsReadLogic(context.Background(), svcCtx)
	resp, err := logic.MarkAsRead(&conversation.MarkAsReadReq{
		ConversationId: 100,
		UserId:         10,
		Seq:            42,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== GetReadStatus Tests ==========

func TestGetReadStatus_Success(t *testing.T) {
	repo := &mockRepo{
		readSeqs: []model.ConvReadSeq{
			{UserID: 10, LastReadSeq: 50},
			{UserID: 20, LastReadSeq: 30},
		},
		countMembers: 2,
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetReadStatusLogic(context.Background(), svcCtx)
	resp, err := logic.GetReadStatus(&conversation.GetReadStatusReq{
		ConversationId: 100,
		MessageId:      40,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(1), resp.ReadCount)
	assert.Equal(t, int32(2), resp.TotalCount)
}

func TestGetReadStatus_NoneRead(t *testing.T) {
	repo := &mockRepo{
		readSeqs: []model.ConvReadSeq{
			{UserID: 10, LastReadSeq: 10},
			{UserID: 20, LastReadSeq: 5},
		},
		countMembers: 2,
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetReadStatusLogic(context.Background(), svcCtx)
	resp, err := logic.GetReadStatus(&conversation.GetReadStatusReq{
		ConversationId: 100,
		MessageId:      42,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(0), resp.ReadCount)
}

// ========== TransferOwner Tests ==========

func TestTransferOwner_Success(t *testing.T) {
	repo := &mockRepo{
		getConv:   &model.Conversation{ID: 100, OwnerID: 10},
		getMember: &model.ConversationMember{UserID: 10, Role: 1},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewTransferOwnerLogic(context.Background(), svcCtx)
	resp, err := logic.TransferOwner(&conversation.TransferOwnerReq{
		ConversationId: 100,
		OperatorId:     10,
		NewOwnerId:     20,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== Set/Delete Announcement Tests ==========

func TestSetAnnouncement_Success(t *testing.T) {
	repo := &mockRepo{getMember: &model.ConversationMember{UserID: 10, Role: 2}}
	svcCtx := newTestSvcCtx(repo)

	logic := NewSetAnnouncementLogic(context.Background(), svcCtx)
	resp, err := logic.SetAnnouncement(&conversation.SetAnnouncementReq{
		ConversationId: 100,
		OperatorId:     10,
		Content:        "hello world",
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteAnnouncement_Success(t *testing.T) {
	repo := &mockRepo{getMember: &model.ConversationMember{UserID: 10, Role: 2}}
	svcCtx := newTestSvcCtx(repo)

	logic := NewDeleteAnnouncementLogic(context.Background(), svcCtx)
	resp, err := logic.DeleteAnnouncement(&conversation.DeleteAnnouncementReq{
		ConversationId: 100,
		OperatorId:     10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== GetConversation Tests ==========

func TestGetConversation_Success(t *testing.T) {
	now := time.Now()
	repo := &mockRepo{
		getConv:        &model.Conversation{ID: 100, Type: 2, Name: "test", CreatedAt: now, UpdatedAt: now},
		getSettingsErr: errors.New("no settings"),
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetConversationLogic(context.Background(), svcCtx)
	resp, err := logic.GetConversation(&conversation.GetConversationReq{
		ConversationId: 100,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int64(100), resp.Conversation.Id)
}

func TestGetConversation_NotFound(t *testing.T) {
	repo := &mockRepo{getConvErr: errors.New("not found")}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetConversationLogic(context.Background(), svcCtx)
	resp, err := logic.GetConversation(&conversation.GetConversationReq{
		ConversationId: 999,
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// ========== GetMemberRole Tests ==========

func TestGetMemberRole_Success(t *testing.T) {
	repo := &mockRepo{
		getMember: &model.ConversationMember{UserID: 10, Role: 1},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewGetMemberRoleLogic(context.Background(), svcCtx)
	resp, err := logic.GetMemberRole(&conversation.GetMemberRoleReq{
		ConversationId: 100,
		UserId:         10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, conversation.MemberRole_MEMBER_ROLE_OWNER, resp.Role)
}

// ========== UpdateConversation Tests ==========

func TestUpdateConversation_Success(t *testing.T) {
	repo := &mockRepo{
		getMember: &model.ConversationMember{Role: 1},
		getConv:   &model.Conversation{ID: 100, Name: "old", Type: 2},
	}
	svcCtx := newTestSvcCtx(repo)

	newName := "new name"
	logic := NewUpdateConversationLogic(context.Background(), svcCtx)
	resp, err := logic.UpdateConversation(&conversation.UpdateConversationReq{
		ConversationId: 100,
		UserId:         10,
		Name:           &newName,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== ListConversations Tests ==========

func TestListConversations_Success(t *testing.T) {
	repo := &mockRepo{
		listConvs: []model.Conversation{{ID: 100}, {ID: 200}},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewListConversationsLogic(context.Background(), svcCtx)
	resp, err := logic.ListConversations(&conversation.ListConversationsReq{
		UserId: 10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Conversations, 2)
}

// ========== UpdateMember Tests ==========

func TestUpdateMember_Success(t *testing.T) {
	repo := &mockRepo{
		getMember: &model.ConversationMember{UserID: 20, Role: 3},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewUpdateMemberLogic(context.Background(), svcCtx)
	newAlias := "buddy"
	resp, err := logic.UpdateMember(&conversation.UpdateMemberReq{
		ConversationId: 100,
		UserId:         20,
		Alias:          &newAlias,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ========== Helpers ==========

func strPtr(s string) *string { return &s }
