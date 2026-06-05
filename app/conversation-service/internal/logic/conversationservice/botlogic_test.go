package conversationservice

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bot methods for mockRepo.
func (m *mockRepo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	return m.getBot, m.getBotErr
}

func (m *mockRepo) GetBotsByIDs(ctx context.Context, ids []int64) ([]model.Bot, error) {
	return nil, nil
}

func (m *mockRepo) AddBot(ctx context.Context, bot *model.ConvBot) error {
	m.addedBot = bot
	return m.addBotErr
}

func (m *mockRepo) AddBotWithMember(ctx context.Context, bot *model.ConvBot, member *model.ConversationMember) error {
	m.addedBot = bot
	m.addedMember = member
	return m.addBotWithMemberErr
}

func (m *mockRepo) RemoveBot(ctx context.Context, convID, botID int64) error {
	m.removedBotConvID = convID
	m.removedBotID = botID
	return m.removeBotErr
}

func (m *mockRepo) RemoveBotWithMember(ctx context.Context, convID, botID int64) error {
	m.removedBotWithMemberConvID = convID
	m.removedBotWithMemberBotID = botID
	return m.removeBotWithMemberErr
}

func (m *mockRepo) RemoveBotMember(ctx context.Context, convID, botID int64) error {
	m.removedBotMemberConvID = convID
	m.removedBotMemberBotID = botID
	return m.removeBotMemberErr
}

func (m *mockRepo) UpdateBot(ctx context.Context, convID, botID int64, triggers []string, settings any) error {
	return m.updateBotErr
}

func (m *mockRepo) ListBotsByConv(ctx context.Context, convID int64) ([]model.ConvBot, error) {
	return nil, nil
}

func (m *mockRepo) GetBotInConv(ctx context.Context, convID, botID int64) (*model.ConvBot, error) {
	if m.getMemberErr != nil {
		return nil, m.getMemberErr
	}
	return &model.ConvBot{
		ConvID:           convID,
		BotID:            botID,
		ResponseTriggers: []string{"mention"},
	}, nil
}

func TestAddBot_AddsConversationBotMemberTransactionally(t *testing.T) {
	repo := &mockRepo{
		getBot: &model.Bot{ID: 200, Name: "assistant", Avatar: "bot.png"},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewAddBotLogic(context.Background(), svcCtx)
	resp, err := logic.AddBot(&conversation.AddBotReq{
		ConversationId: 100,
		BotId:          200,
		OperatorId:     10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	require.NotNil(t, repo.addedBot)
	assert.Equal(t, int64(100), repo.addedBot.ConvID)
	assert.Equal(t, int64(200), repo.addedBot.BotID)
	assert.Equal(t, []string{"mention"}, repo.addedBot.ResponseTriggers)
	require.NotNil(t, repo.addedMember)
	assert.Equal(t, int64(100), repo.addedMember.ConvID)
	assert.Equal(t, int64(200), repo.addedMember.UserID)
	assert.Equal(t, model.MemberTypeBot, repo.addedMember.MemberType)
	assert.Equal(t, int64(200), repo.addedMember.BotID)
	assert.Equal(t, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER), repo.addedMember.Role)
	assert.Empty(t, repo.memberCountDeltas)
}
func TestAddBot_CustomTriggers(t *testing.T) {
	repo := &mockRepo{
		getBot: &model.Bot{ID: 200, Name: "assistant", Avatar: "bot.png"},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewAddBotLogic(context.Background(), svcCtx)
	resp, err := logic.AddBot(&conversation.AddBotReq{
		ConversationId:   100,
		BotId:            200,
		OperatorId:       10,
		ResponseTriggers: []string{"mention", "always"},
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(0), resp.Code)
	assert.Equal(t, []string{"mention", "always"}, repo.addedBot.ResponseTriggers)
}

func TestAddBot_DefaultTriggers(t *testing.T) {
	repo := &mockRepo{
		getBot: &model.Bot{ID: 200, Name: "assistant", Avatar: "bot.png"},
	}
	svcCtx := newTestSvcCtx(repo)

	logic := NewAddBotLogic(context.Background(), svcCtx)
	resp, err := logic.AddBot(&conversation.AddBotReq{
		ConversationId: 100,
		BotId:          200,
		OperatorId:     10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestRemoveBot_CallsRemoveBotWithMember(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewRemoveBotLogic(context.Background(), svcCtx)
	resp, err := logic.RemoveBot(&conversation.RemoveBotReq{
		ConversationId: 100,
		BotId:          200,
		OperatorId:     10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int64(100), repo.removedBotWithMemberConvID)
	assert.Equal(t, int64(200), repo.removedBotWithMemberBotID)
	assert.Zero(t, repo.removedBotConvID)
	assert.Zero(t, repo.removedBotMemberConvID)
	assert.Empty(t, repo.memberCountDeltas)
}

func TestRemoveBot_WithMemberNoExistingRelationIsSuccess(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewRemoveBotLogic(context.Background(), svcCtx)
	resp, err := logic.RemoveBot(&conversation.RemoveBotReq{
		ConversationId: 100,
		BotId:          200,
		OperatorId:     10,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestUpdateBot_Success(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewUpdateBotLogic(context.Background(), svcCtx)
	resp, err := logic.UpdateBot(&conversation.UpdateBotReq{
		ConversationId:   100,
		BotId:            200,
		ResponseTriggers: []string{"keyword:帮助"},
		BotSettings:      `{"temperature":0.5}`,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListBots_Success(t *testing.T) {
	repo := &mockRepo{}
	svcCtx := newTestSvcCtx(repo)

	logic := NewListBotsLogic(context.Background(), svcCtx)
	resp, err := logic.ListBots(&conversation.ListBotsReq{
		ConversationId: 100,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Bots)
}
