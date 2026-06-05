package botplatform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/repo"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSvcCtx() *svc.ServiceContext {
	return &svc.ServiceContext{}
}

func TestCreateBotValidation(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewCreateBotLogic(context.Background(), svcCtx)

	_, err := logic.CreateBot(&pb.CreateBotReq{
		OwnerId: 10,
		Name:    "testBot",
		Type:    "official",
	})
	require.Error(t, err) // nil repo

	_, err = logic.CreateBot(&pb.CreateBotReq{
		OwnerId: 10,
		Type:    "self_deployed",
	})
	require.Error(t, err) // missing name

	_, err = logic.CreateBot(&pb.CreateBotReq{
		Name: "bad",
		Type: "invalid",
	})
	require.Error(t, err) // invalid type

	_, err = logic.CreateBot(&pb.CreateBotReq{
		OwnerId:  10,
		Name:     "whBot",
		Type:     "third_party",
		ConnMode: "webhook",
	})
	require.Error(t, err) // missing callback_url

	_, err = logic.CreateBot(&pb.CreateBotReq{
		OwnerId: 0,
		Name:    "noOwner",
		Type:    "self_deployed",
	})
	require.Error(t, err) // user bot needs owner_id
}

func TestNormalizeBotType(t *testing.T) {
	assert.Equal(t, TypeOfficial, NormalizeBotType("BOT_TYPE_OFFICIAL"))
	assert.Equal(t, TypeOfficial, NormalizeBotType("official"))
	assert.Equal(t, TypeSelfDeployed, NormalizeBotType("BOT_TYPE_SELF_DEPLOYED"))
	assert.Equal(t, TypeSelfDeployed, NormalizeBotType("self_deployed"))
	assert.Equal(t, TypeThirdParty, NormalizeBotType("BOT_TYPE_THIRD_PARTY"))
	assert.Equal(t, TypeThirdParty, NormalizeBotType("third_party"))
}

func TestNormalizeConnMode(t *testing.T) {
	assert.Equal(t, ConnModeWS, NormalizeConnMode("CONN_MODE_WS"))
	assert.Equal(t, ConnModeWS, NormalizeConnMode("ws"))
	assert.Equal(t, ConnModeWebhook, NormalizeConnMode("CONN_MODE_WEBHOOK"))
	assert.Equal(t, ConnModeWebhook, NormalizeConnMode("webhook"))
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-secret-12345"
	message := []byte(`{"text":"hello world"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	validSig := hex.EncodeToString(mac.Sum(nil))

	err := verifyWebhookSignature(message, validSig, time.Now().Unix(), secret)
	assert.NoError(t, err)

	err = verifyWebhookSignature(message, "bad-sig", time.Now().Unix(), secret)
	assert.Error(t, err)

	err = verifyWebhookSignature(message, validSig, time.Now().Add(-10*time.Minute).Unix(), secret)
	assert.Error(t, err)
}

func TestModelBotToProto(t *testing.T) {
	now := time.Now()
	bot := &model.Bot{
		ID:          1,
		OwnerID:     10,
		Name:        "test",
		Type:        TypeOfficial,
		Status:      "active",
		Temperature: 0.7,
		BotTags:     []string{"tag1"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	pbBot := modelBotToProto(bot)
	assert.Equal(t, int64(1), pbBot.Id)
	assert.Equal(t, int64(10), pbBot.OwnerId)
	assert.Equal(t, "test", pbBot.Name)
	assert.Equal(t, TypeOfficial, pbBot.Type)
	assert.Equal(t, "active", pbBot.Status)
	assert.False(t, pbBot.HasWebhookSecret)
	assert.False(t, pbBot.HasAppSecret)
	assert.Equal(t, now.Unix(), pbBot.CreatedAt)
	assert.Equal(t, now.Unix(), pbBot.UpdatedAt)
}

func TestCanWrite(t *testing.T) {
	bot := &model.Bot{OwnerID: 100}
	assert.True(t, canWrite(bot, 100))
	assert.False(t, canWrite(bot, 200))

	platformBot := &model.Bot{OwnerID: 0}
	assert.False(t, canWrite(platformBot, 100))
	assert.False(t, canWrite(platformBot, 0))
}

func TestCreateOfficialInstanceLeavesEmptyCapabilitiesUnset(t *testing.T) {
	fake := &officialInstanceRepo{}
	logic := NewListBotsLogic(context.Background(), &svc.ServiceContext{Repo: fake})
	tpl := &model.Bot{
		ID:                 123,
		Name:               "智能问答助手",
		Type:               TypeOfficial,
		TemplateID:         TemplateQA,
		Status:             "active",
		UsePlatformModel:   true,
		SystemPrompt:       "hello",
		EnableKnowledge:    true,
		Temperature:        0.7,
		MaxContextMessages: 10,
		StreamingEnabled:   true,
		ConnMode:           ConnModeWS,
	}

	require.NoError(t, logic.createOfficialInstance(456, tpl))
	require.NotNil(t, fake.created)
	assert.Nil(t, fake.created.Capabilities)
}

func TestUpdateOfficialBotSavesStreamingEnabled(t *testing.T) {
	repo := &officialInstanceRepo{
		bot: &model.Bot{
			ID:               123,
			OwnerID:          456,
			Type:             TypeOfficial,
			TemplateID:       TemplateQA,
			StreamingEnabled: true,
		},
	}
	logic := NewUpdateBotLogic(context.Background(), &svc.ServiceContext{Repo: repo})

	_, err := logic.UpdateBot(&pb.UpdateBotReq{
		BotId:            123,
		UserId:           456,
		StreamingEnabled: false,
	})

	require.NoError(t, err)
	assert.Contains(t, repo.updates, "streaming_enabled")
	assert.Equal(t, false, repo.updates["streaming_enabled"])
}

type officialInstanceRepo struct {
	created *model.Bot
	bot     *model.Bot
	updates map[string]any
}

func (r *officialInstanceRepo) CreateBot(ctx context.Context, bot *model.Bot) error {
	r.created = bot
	return nil
}

func (r *officialInstanceRepo) UpdateBot(ctx context.Context, botID int64, updates map[string]any) error {
	r.updates = updates
	if r.bot != nil {
		if v, ok := updates["streaming_enabled"].(bool); ok {
			r.bot.StreamingEnabled = v
		}
	}
	return nil
}

func (r *officialInstanceRepo) DeleteBot(ctx context.Context, botID int64) error { return nil }

func (r *officialInstanceRepo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	if r.bot != nil {
		return r.bot, nil
	}
	return nil, nil
}

func (r *officialInstanceRepo) GetBotsByIDs(ctx context.Context, botIDs []int64) ([]model.Bot, error) {
	return nil, nil
}

func (r *officialInstanceRepo) ListBotsByOwner(ctx context.Context, ownerID int64, status string, offset, limit int) ([]model.Bot, int64, error) {
	return nil, 0, nil
}

func (r *officialInstanceRepo) ListOfficialTemplates(ctx context.Context) ([]model.Bot, error) {
	return nil, nil
}

func (r *officialInstanceRepo) GetOfficialInstance(ctx context.Context, ownerID int64, templateID int64) (*model.Bot, error) {
	return nil, nil
}

func (r *officialInstanceRepo) NextID(ctx context.Context) (int64, error) { return 999, nil }

func (r *officialInstanceRepo) CreateMcpServer(ctx context.Context, srv *model.McpServer) error {
	return nil
}

func (r *officialInstanceRepo) UpdateMcpServer(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}

func (r *officialInstanceRepo) DeleteMcpServer(ctx context.Context, id int64) error { return nil }

func (r *officialInstanceRepo) GetMcpServer(ctx context.Context, id int64) (*model.McpServer, error) {
	return nil, nil
}

func (r *officialInstanceRepo) ListMcpServers(ctx context.Context, status string, offset, limit int) ([]model.McpServer, int64, error) {
	return nil, 0, nil
}

func (r *officialInstanceRepo) ListUserMcpServers(ctx context.Context, userID int64, status string, offset, limit int) ([]model.McpServer, int64, error) {
	return nil, 0, nil
}

func (r *officialInstanceRepo) AssignMcpToBot(ctx context.Context, assoc *model.BotMcpServer) error {
	return nil
}

func (r *officialInstanceRepo) UnassignMcpFromBot(ctx context.Context, botID, mcpServerID int64) error {
	return nil
}

func (r *officialInstanceRepo) ListBotMcpServers(ctx context.Context, botID int64) ([]model.BotMcpServer, error) {
	return nil, nil
}

func (r *officialInstanceRepo) UpdateBotMcpServer(ctx context.Context, botID, mcpServerID int64, enabled bool) error {
	return nil
}

func (r *officialInstanceRepo) GetBotMcpServer(ctx context.Context, botID, mcpServerID int64) (*model.BotMcpServer, error) {
	return nil, nil
}

func (r *officialInstanceRepo) ResolveModelID(ctx context.Context, modelName string) (int64, error) {
	return 14, nil // qwen-plus
}

func (r *officialInstanceRepo) GetModelOwner(ctx context.Context, modelID int64) (int64, error) {
	return 0, nil
}

func (r *officialInstanceRepo) UpsertMcpTools(ctx context.Context, serverID int64, tools []model.McpTool) error {
	return nil
}

func (r *officialInstanceRepo) ListMcpTools(ctx context.Context, serverID int64) ([]model.McpTool, error) {
	return nil, nil
}

func (r *officialInstanceRepo) ListActiveWebhookBots(ctx context.Context) ([]repo.WebhookBotSecret, error) {
	return nil, nil
}
