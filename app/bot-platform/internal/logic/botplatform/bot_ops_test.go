package botplatform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/repo"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	pbcommon "github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// enhancedMockRepo extends officialInstanceRepo with richer test scenarios.
type enhancedMockRepo struct {
	*officialInstanceRepo
	bots      map[int64]*model.Bot
	templates []model.Bot
	nextIDVal int64
	err       error // generic error for methods not explicitly set
}

func newEnhancedMockRepo() *enhancedMockRepo {
	return &enhancedMockRepo{
		officialInstanceRepo: &officialInstanceRepo{
			updates: make(map[string]any),
		},
		bots:      make(map[int64]*model.Bot),
		templates: []model.Bot{},
	}
}

func (m *enhancedMockRepo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	if m.err != nil {
		return nil, m.err
	}
	bot, ok := m.bots[botID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return bot, nil
}

func (m *enhancedMockRepo) CreateBot(ctx context.Context, bot *model.Bot) error {
	m.officialInstanceRepo.created = bot
	m.bots[bot.ID] = bot
	return nil
}

func (m *enhancedMockRepo) DeleteBot(ctx context.Context, botID int64) error {
	delete(m.bots, botID)
	return nil
}

func (m *enhancedMockRepo) UpdateBot(ctx context.Context, botID int64, updates map[string]any) error {
	m.updates = updates
	if bot, ok := m.bots[botID]; ok {
		if v, ok := updates["name"].(string); ok {
			bot.Name = v
		}
		if v, ok := updates["streaming_enabled"].(bool); ok {
			bot.StreamingEnabled = v
		}
		if v, ok := updates["callback_url"].(string); ok {
			bot.CallbackURL = v
		}
		if v, ok := updates["system_prompt"].(string); ok {
			bot.SystemPrompt = v
		}
		if v, ok := updates["temperature"].(float64); ok {
			bot.Temperature = v
		}
		if v, ok := updates["enable_knowledge"].(bool); ok {
			bot.EnableKnowledge = v
		}
		if v, ok := updates["model_name"].(string); ok {
			bot.ModelName = v
		}
	}
	return nil
}

func (m *enhancedMockRepo) ListBotsByOwner(ctx context.Context, ownerID int64, status string, offset, limit int) ([]model.Bot, int64, error) {
	var result []model.Bot
	for _, bot := range m.bots {
		if bot.OwnerID == ownerID {
			result = append(result, *bot)
		}
	}
	return result, int64(len(result)), nil
}

func (m *enhancedMockRepo) ListOfficialTemplates(ctx context.Context) ([]model.Bot, error) {
	return m.templates, nil
}

func (m *enhancedMockRepo) GetOfficialInstance(ctx context.Context, ownerID int64, templateID int64) (*model.Bot, error) {
	return nil, gorm.ErrRecordNotFound
}

func (m *enhancedMockRepo) NextID(ctx context.Context) (int64, error) {
	return m.nextIDVal, nil
}

func (m *enhancedMockRepo) GetBotsByIDs(ctx context.Context, botIDs []int64) ([]model.Bot, error) {
	var result []model.Bot
	for _, id := range botIDs {
		if bot, ok := m.bots[id]; ok {
			result = append(result, *bot)
		}
	}
	return result, nil
}

func (m *enhancedMockRepo) ResolveModelID(ctx context.Context, modelName string) (int64, error) {
	return 14, nil // qwen-plus
}

func (m *enhancedMockRepo) GetModelOwner(ctx context.Context, modelID int64) (int64, error) {
	if modelID == 14 {
		return 0, nil
	}
	return 100, nil
}

func (m *enhancedMockRepo) ListActiveWebhookBots(ctx context.Context) ([]repo.WebhookBotSecret, error) {
	return nil, nil
}

func TestGetBot_Normal(t *testing.T) {
	now := time.Now()
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{
		ID: 1, OwnerID: 10, Name: "test-bot", Type: "self_deployed",
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewGetBotLogic(context.Background(), svcCtx)

	resp, err := logic.GetBot(&pb.GetBotReq{BotId: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Id)
	assert.Equal(t, "test-bot", resp.Name)
	assert.Equal(t, "self_deployed", resp.Type)
}

func TestGetBot_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{Repo: newEnhancedMockRepo()}
	logic := NewGetBotLogic(context.Background(), svcCtx)

	_, err := logic.GetBot(&pb.GetBotReq{BotId: 999})
	require.Error(t, err)
	assert.Equal(t, ErrBotNotFound, err)
}

func TestGetBot_DBError(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.err = gorm.ErrInvalidDB
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewGetBotLogic(context.Background(), svcCtx)

	_, err := logic.GetBot(&pb.GetBotReq{BotId: 1})
	require.Error(t, err)
}

func TestDeleteBot_Normal(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "test", Type: "self_deployed"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewDeleteBotLogic(context.Background(), svcCtx)

	resp, err := logic.DeleteBot(&pb.DeleteBotReq{BotId: 1, UserId: 10})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
	_, ok := repo.bots[1]
	assert.False(t, ok, "bot should be deleted")
}

func TestDeleteBot_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{Repo: newEnhancedMockRepo()}
	logic := NewDeleteBotLogic(context.Background(), svcCtx)

	_, err := logic.DeleteBot(&pb.DeleteBotReq{BotId: 999, UserId: 10})
	require.Error(t, err)
	assert.Equal(t, ErrBotNotFound, err)
}

func TestDeleteBot_Forbidden_NotOwner(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "test", Type: "self_deployed"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewDeleteBotLogic(context.Background(), svcCtx)

	_, err := logic.DeleteBot(&pb.DeleteBotReq{BotId: 1, UserId: 999})
	require.Error(t, err)
	assert.Equal(t, ErrBotForbidden, err)
}

func TestDeleteBot_Forbidden_OfficialInstance(t *testing.T) {
	settings, _ := json.Marshal(map[string]any{"official_instance": true})
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "test", Type: "official", Settings: settings}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewDeleteBotLogic(context.Background(), svcCtx)

	_, err := logic.DeleteBot(&pb.DeleteBotReq{BotId: 1, UserId: 10})
	require.Error(t, err)
	assert.Equal(t, ErrBotForbidden, err)
}

func TestListBots_Normal(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.templates = []model.Bot{} // no templates to auto-instance
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "bot-a", Type: "self_deployed", Status: "active"}
	repo.bots[2] = &model.Bot{ID: 2, OwnerID: 10, Name: "bot-b", Type: "self_deployed", Status: "active"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewListBotsLogic(context.Background(), svcCtx)

	resp, err := logic.ListBots(&pb.ListBotsReq{
		OwnerId: 10,
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Bots, 2)
	assert.Equal(t, int64(2), resp.Pagination.Total)
}

func TestListBots_DefaultPagination(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.templates = []model.Bot{}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewListBotsLogic(context.Background(), svcCtx)

	resp, err := logic.ListBots(&pb.ListBotsReq{OwnerId: 10})
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Pagination.Page)
	assert.Equal(t, int32(20), resp.Pagination.PageSize)
}

func TestListBots_PageSizeCap(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.templates = []model.Bot{}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewListBotsLogic(context.Background(), svcCtx)

	resp, err := logic.ListBots(&pb.ListBotsReq{
		OwnerId:    10,
		Pagination: &pbcommon.Pagination{Page: 1, PageSize: 200},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(100), resp.Pagination.PageSize)
}

func TestListBots_Empty(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.templates = []model.Bot{}
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 99, Name: "other", Type: "official", Status: "active"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewListBotsLogic(context.Background(), svcCtx)

	resp, err := logic.ListBots(&pb.ListBotsReq{OwnerId: 10})
	require.NoError(t, err)
	assert.Empty(t, resp.Bots)
	assert.Equal(t, int64(0), resp.Pagination.Total)
}

func TestListBots_WithAutoInstance(t *testing.T) {
	settings, _ := json.Marshal(map[string]any{"prompt_locale": "zh-CN"})
	repo := newEnhancedMockRepo()
	repo.templates = []model.Bot{
		{ID: 100, OwnerID: 0, Name: "智能问答助手", Type: "official", TemplateID: "qa",
			Status: "active", UsePlatformModel: true, StreamingEnabled: true, ConnMode: "ws",
			Settings: settings},
	}
	repo.nextIDVal = 200
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewListBotsLogic(context.Background(), svcCtx)

	resp, err := logic.ListBots(&pb.ListBotsReq{OwnerId: 10})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	// verify the auto-created instance
	instance, ok := repo.bots[200]
	require.True(t, ok, "auto instance should be created")
	assert.Equal(t, int64(10), instance.OwnerID)
	assert.Equal(t, "智能问答助手", instance.Name)
}

func TestBatchGetBots_Normal(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "bot-a", Type: "self_deployed", Status: "active"}
	repo.bots[2] = &model.Bot{ID: 2, OwnerID: 10, Name: "bot-b", Type: "self_deployed", Status: "active"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewBatchGetBotsLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetBots(&pb.BatchGetBotsReq{BotIds: []int64{1, 2, 999}})
	require.NoError(t, err)
	assert.Len(t, resp.Bots, 2)
}

func TestBatchGetBots_Empty(t *testing.T) {
	svcCtx := &svc.ServiceContext{Repo: newEnhancedMockRepo()}
	logic := NewBatchGetBotsLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetBots(&pb.BatchGetBotsReq{BotIds: []int64{}})
	require.NoError(t, err)
	assert.Empty(t, resp.Bots)
}

func TestGetBotWebhookConfig_Normal(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{
		ID: 1, OwnerID: 10, Name: "webhook-bot", Type: "third_party",
		ConnMode: "webhook", CallbackURL: "https://example.com/cb",
		WebhookSecret: "secret123", Status: "active",
	}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewGetBotWebhookConfigLogic(context.Background(), svcCtx)

	resp, err := logic.GetBotWebhookConfig(&pb.GetBotWebhookConfigReq{BotId: 1})
	require.NoError(t, err)
	assert.Equal(t, "webhook", resp.ConnMode)
	assert.Equal(t, "https://example.com/cb", resp.CallbackUrl)
	assert.Equal(t, "secret123", resp.WebhookSecret)
	assert.Equal(t, "active", resp.Status)
}

func TestGetBotWebhookConfig_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{Repo: newEnhancedMockRepo()}
	logic := NewGetBotWebhookConfigLogic(context.Background(), svcCtx)

	_, err := logic.GetBotWebhookConfig(&pb.GetBotWebhookConfigReq{BotId: 999})
	require.Error(t, err)
}

func TestRotateSecret_Normal(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewRotateSecretLogic(context.Background(), svcCtx)

	resp, err := logic.RotateSecret(&pb.RotateSecretReq{BotId: 1})
	require.NoError(t, err)
	assert.Equal(t, "new-secret", resp.WebhookSecret)
	assert.Equal(t, "new-app-secret", resp.AppSecret)
}

func TestIssueBotToken_Normal(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewIssueBotTokenLogic(context.Background(), svcCtx)

	resp, err := logic.IssueBotToken(&pb.IssueBotTokenReq{BotId: 1})
	require.NoError(t, err)
	assert.Equal(t, "bot-token", resp.Token)
	assert.Equal(t, int64(0), resp.ExpiresAt)
}

func TestValidateBotToken_Normal(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewValidateBotTokenLogic(context.Background(), svcCtx)

	resp, err := logic.ValidateBotToken(&pb.ValidateBotTokenReq{Token: "test-token"})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, "official", resp.Type)
}

func TestIsOfficialInstance(t *testing.T) {
	// nil bot
	assert.False(t, isOfficialInstance(nil))

	// no settings
	bot := &model.Bot{}
	assert.False(t, isOfficialInstance(bot))

	// settings with official_instance=true
	settings, _ := json.Marshal(map[string]any{"official_instance": true})
	bot.Settings = settings
	assert.True(t, isOfficialInstance(bot))

	// settings with official_instance=false
	settings, _ = json.Marshal(map[string]any{"official_instance": false})
	bot.Settings = settings
	assert.False(t, isOfficialInstance(bot))

	// settings missing official_instance key
	settings, _ = json.Marshal(map[string]any{"other_key": "value"})
	bot.Settings = settings
	assert.False(t, isOfficialInstance(bot))

	// invalid JSON settings
	bot.Settings = []byte("{invalid")
	assert.False(t, isOfficialInstance(bot))
}

func TestDerefInt64(t *testing.T) {
	val := int64(42)
	assert.Equal(t, int64(42), derefInt64(&val))
	assert.Equal(t, int64(0), derefInt64(nil))
}

func TestAbs(t *testing.T) {
	assert.Equal(t, int64(5), abs(5))
	assert.Equal(t, int64(5), abs(-5))
	assert.Equal(t, int64(0), abs(0))
}

func TestUpdateBot_Normal_Official(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{
		ID: 1, OwnerID: 10, Name: "qa-bot", Type: "official",
		TemplateID: "qa", Status: "active",
	}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewUpdateBotLogic(context.Background(), svcCtx)

	resp, err := logic.UpdateBot(&pb.UpdateBotReq{
		BotId:            1,
		UserId:           10,
		Name:             "updated-qa-bot",
		StreamingEnabled: false,
	})
	require.NoError(t, err)
	assert.Equal(t, "updated-qa-bot", resp.Name)
}

func TestUpdateBot_Normal_SelfDeployed(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{
		ID: 1, OwnerID: 10, Name: "my-bot", Type: "self_deployed", Status: "active",
	}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewUpdateBotLogic(context.Background(), svcCtx)

	resp, err := logic.UpdateBot(&pb.UpdateBotReq{
		BotId:            1,
		UserId:           10,
		Name:             "new-name",
		SystemPrompt:     "You are a helpful bot",
		Temperature:      0.5,
		EnableKnowledge:  true,
		StreamingEnabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "new-name", resp.Name)
	assert.Contains(t, repo.updates, "system_prompt")
	assert.Contains(t, repo.updates, "temperature")
	assert.Contains(t, repo.updates, "enable_knowledge")
}

func TestUpdateBot_Normal_ThirdParty(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{
		ID: 1, OwnerID: 10, Name: "webhook-bot", Type: "third_party",
		SubType: "webhook", ConnMode: "webhook", Status: "active",
	}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewUpdateBotLogic(context.Background(), svcCtx)

	resp, err := logic.UpdateBot(&pb.UpdateBotReq{
		BotId:       1,
		UserId:      10,
		CallbackUrl: "https://new-cb.example.com",
		SubType:     "webhook",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://new-cb.example.com", resp.CallbackUrl)
}

func TestUpdateBot_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{Repo: newEnhancedMockRepo()}
	logic := NewUpdateBotLogic(context.Background(), svcCtx)

	_, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: 999, UserId: 10})
	require.Error(t, err)
	assert.Equal(t, ErrBotNotFound, err)
}

func TestUpdateBot_Forbidden(t *testing.T) {
	repo := newEnhancedMockRepo()
	repo.bots[1] = &model.Bot{ID: 1, OwnerID: 10, Name: "test", Type: "self_deployed", Status: "active"}
	svcCtx := &svc.ServiceContext{Repo: repo}
	logic := NewUpdateBotLogic(context.Background(), svcCtx)

	_, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: 1, UserId: 999})
	require.Error(t, err)
	assert.Equal(t, ErrBotForbidden, err)
}

func TestCanWrite_PlatformBot(t *testing.T) {
	bot := &model.Bot{OwnerID: 0}
	assert.False(t, canWrite(bot, 100))
	assert.False(t, canWrite(bot, 0))
}

func TestModelBotToProto_Nil(t *testing.T) {
	assert.Nil(t, modelBotToProto(nil))
}

func TestModelBotToProto_AllFields(t *testing.T) {
	now := time.Now()
	bot := &model.Bot{
		ID: 1, OwnerID: 10, Name: "full-bot", Type: "self_deployed",
		Status: "active", UsePlatformModel: true,
		ModelName: "gpt-4", ModelID: 100, BaseURL: "https://api.example.com",
		SystemPrompt: "You are a bot", Persona: "friendly",
		EnableKnowledge: true, Temperature: 0.5,
		MaxContextMessages: 20, StreamingEnabled: false,
		MemoryModelName: "gpt-3.5", MemoryModelID: 50,
		MemoryUsePlatformModel: false, MemoryLimit: 100,
		ConnMode: "ws", WebhookSecret: "wh-secret",
		CallbackURL: "https://cb.example.com", AppSecretHash: "app-secret-hash",
		SubType: "ws", TemplateID: "qa",
		BotTags: []string{"tag1", "tag2"},
		CreatedAt: now, UpdatedAt: now,
	}
	pbBot := modelBotToProto(bot)
	assert.Equal(t, int64(1), pbBot.Id)
	assert.Equal(t, "full-bot", pbBot.Name)
	assert.True(t, pbBot.HasWebhookSecret)
	assert.True(t, pbBot.HasAppSecret)
	assert.Equal(t, []string{"tag1", "tag2"}, pbBot.BotTags)
	assert.Equal(t, now.Unix(), pbBot.CreatedAt)
}

func TestNormalizeBotType_EdgeCases(t *testing.T) {
	assert.Equal(t, "self_deployed", NormalizeBotType("self_deployed"))
	assert.Equal(t, "official", NormalizeBotType("BOT_TYPE_OFFICIAL"))
	assert.Equal(t, "third_party", NormalizeBotType("third_party"))
	assert.Equal(t, "unknown", NormalizeBotType("unknown"))
}

func TestNormalizeConnMode_EdgeCases(t *testing.T) {
	assert.Equal(t, "ws", NormalizeConnMode("ws"))
	assert.Equal(t, "ws", NormalizeConnMode("unknown")) // default to ws
	assert.Equal(t, "webhook", NormalizeConnMode("CONN_MODE_WEBHOOK"))
}

func TestVerifyWebhookSignature_Expired(t *testing.T) {
	secret := "test-secret"
	message := []byte(`{"text":"hello"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	validSig := hex.EncodeToString(mac.Sum(nil))

	// 10 minutes ago -> expired
	err := verifyWebhookSignature(message, validSig, time.Now().Add(-10*time.Minute).Unix(), secret)
	assert.Error(t, err)
}

func TestVerifyWebhookSignature_BadSignature(t *testing.T) {
	err := verifyWebhookSignature([]byte("test"), "bad-sig", time.Now().Unix(), "secret")
	assert.Error(t, err)
}

func TestCloneJSONMap(t *testing.T) {
	assert.Equal(t, map[string]any{}, cloneJSONMap(nil))
	assert.Equal(t, map[string]any{}, cloneJSONMap([]byte{}))
	assert.Equal(t, map[string]any{}, cloneJSONMap([]byte("invalid")))
	result := cloneJSONMap([]byte(`{"key":"value","num":42}`))
	assert.Equal(t, "value", result["key"])
	assert.Equal(t, float64(42), result["num"])
}
