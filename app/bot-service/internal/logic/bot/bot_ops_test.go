package bot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type enhancedMockRepo struct {
	*officialInstanceRepo
	bots      map[string]*model.Bot
	templates []model.Bot
	servers   map[string]*model.McpServer
	err       error
}

func newEnhancedMockRepo() *enhancedMockRepo {
	return &enhancedMockRepo{officialInstanceRepo: &officialInstanceRepo{}, bots: make(map[string]*model.Bot), servers: make(map[string]*model.McpServer)}
}
func (m *enhancedMockRepo) GetBot(ctx context.Context, botID string) (*model.Bot, error) {
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
	m.created = bot
	m.bots[bot.ID] = bot
	return nil
}
func (m *enhancedMockRepo) DeleteBot(ctx context.Context, botID string) error {
	delete(m.bots, botID)
	return nil
}
func (m *enhancedMockRepo) UpdateBot(ctx context.Context, botID string, updates map[string]any) error {
	bot, ok := m.bots[botID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	for key, value := range updates {
		switch key {
		case "name":
			bot.Name = value.(string)
		case "streaming_enabled":
			bot.StreamingEnabled = value.(bool)
		case "callback_url":
			bot.CallbackURL = value.(string)
		case "system_prompt":
			bot.SystemPrompt = value.(string)
		case "temperature":
			bot.Temperature = value.(float64)
		case "enable_knowledge":
			bot.EnableKnowledge = value.(bool)
		case "model_name":
			bot.ModelName = value.(string)
		case "model_id":
			bot.ModelID, _ = value.(*string)
		case "memory_model_id":
			bot.MemoryModelID, _ = value.(*string)
		case "memory_embedding_model_id":
			bot.MemoryEmbeddingModelID, _ = value.(*string)
		case "status":
			bot.Status = value.(string)
		case "webhook_secret":
			bot.WebhookSecret = value.(string)
		case "app_secret_hash":
			bot.AppSecretHash = value.(string)
		case "settings":
			bot.Settings = value.([]byte)
		case "sub_type":
			bot.SubType = value.(string)
		case "conn_mode":
			bot.ConnMode = value.(string)
		}
	}
	return nil
}
func (m *enhancedMockRepo) ListBotsByOwner(ctx context.Context, ownerType string, ownerID *string, status string, offset, limit int) ([]model.Bot, int64, error) {
	var result []model.Bot
	for _, bot := range m.bots {
		if bot.OwnerType == ownerType && sameOwner(bot.OwnerID, ownerID) && (status == "" || status == bot.Status) {
			result = append(result, *bot)
		}
	}
	return result, int64(len(result)), nil
}
func (m *enhancedMockRepo) ListOfficialTemplates(ctx context.Context) ([]model.Bot, error) {
	return m.templates, nil
}
func (m *enhancedMockRepo) GetOfficialInstance(ctx context.Context, ownerID, templateID string) (*model.Bot, error) {
	for _, bot := range m.bots {
		if bot.OwnerType == "user" && bot.OwnerID != nil && *bot.OwnerID == ownerID && cloneJSONMap(bot.Settings)["official_template_id"] == templateID {
			return bot, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *enhancedMockRepo) GetBotsByIDs(ctx context.Context, botIDs []string) ([]model.Bot, error) {
	var result []model.Bot
	for _, id := range botIDs {
		if bot, ok := m.bots[id]; ok {
			result = append(result, *bot)
		}
	}
	return result, nil
}

func TestGetBot_NotFound(t *testing.T) {
	logic := NewGetBotLogic(t.Context(), &svc.ServiceContext{Repo: newEnhancedMockRepo()})
	_, err := logic.GetBot(&pb.GetBotReq{BotId: testBotID})
	assert.ErrorIs(t, err, ErrBotNotFound)
}
func TestGetBot_DBError(t *testing.T) {
	r := newEnhancedMockRepo()
	r.err = gorm.ErrInvalidDB
	_, err := NewGetBotLogic(t.Context(), &svc.ServiceContext{Repo: r}).GetBot(&pb.GetBotReq{BotId: testBotID})
	require.Error(t, err)
}
func TestDeleteBot_Normal(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "self_deployed"}
	s := &svc.ServiceContext{Repo: r}
	_, err := NewDeleteBotLogic(testCaller(t), s).DeleteBot(&pb.DeleteBotReq{BotId: testBotID, UserId: testOwnerID})
	require.NoError(t, err)
	_, err = NewGetBotLogic(t.Context(), s).GetBot(&pb.GetBotReq{BotId: testBotID})
	assert.ErrorIs(t, err, ErrBotNotFound)
}
func TestDeleteBot_Forbidden_NotOwner(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOtherOwnerID), Type: "self_deployed"}
	_, err := NewDeleteBotLogic(testCaller(t), &svc.ServiceContext{Repo: r}).DeleteBot(&pb.DeleteBotReq{BotId: testBotID, UserId: testOwnerID})
	assert.ErrorIs(t, err, ErrBotForbidden)
	_, err = r.GetBot(t.Context(), testBotID)
	require.NoError(t, err)
}
func TestDeleteBot_Forbidden_OfficialInstance(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "official", Settings: []byte(`{"official_instance":true}`)}
	_, err := NewDeleteBotLogic(testCaller(t), &svc.ServiceContext{Repo: r}).DeleteBot(&pb.DeleteBotReq{BotId: testBotID, UserId: testOwnerID})
	assert.ErrorIs(t, err, ErrBotForbidden)
}
func TestListBots_WithAutoInstance(t *testing.T) {
	r := newEnhancedMockRepo()
	r.templates = []model.Bot{{ID: testBotID, OwnerType: "platform", Type: "official", TemplateID: "qa", ModelID: stringPtr(testModelID), Settings: []byte(`{"prompt_locale":"zh-CN"}`)}}
	logic := NewListBotsLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	request := &pb.ListBotsReq{OwnerType: "user", OwnerId: stringPtr(testOwnerID)}
	first, err := logic.ListBots(request)
	require.NoError(t, err)
	require.Len(t, first.Bots, 1)
	assert.NotEqual(t, testBotID, first.Bots[0].Id)
	assert.Equal(t, testModelID, first.Bots[0].GetModelId())
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(first.Bots[0].Settings), &settings))
	assert.Equal(t, testBotID, settings["official_template_id"])
	second, err := logic.ListBots(request)
	require.NoError(t, err)
	require.Len(t, second.Bots, 1)
	assert.Equal(t, first.Bots[0].Id, second.Bots[0].Id)
}
func TestRotateSecret_Normal(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), WebhookSecret: "previous-secret"}
	rotated, err := NewRotateSecretLogic(testCaller(t), &svc.ServiceContext{Repo: r}).RotateSecret(&pb.RotateSecretReq{BotId: testBotID, UserId: testOwnerID})
	require.NoError(t, err)
	body := []byte(`{"text":"hello"}`)
	mac := hmac.New(sha256.New, []byte(rotated.WebhookSecret))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	require.NoError(t, verifyWebhookSignature(body, signature, time.Now().Unix(), r.bots[testBotID].WebhookSecret))
	assert.Error(t, verifyWebhookSignature(body, signature, time.Now().Unix(), "previous-secret"))
	assert.Equal(t, hashSecret(rotated.AppSecret), r.bots[testBotID].AppSecretHash)
}
func TestIssueBotToken_Normal(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "self_deployed", Status: "active"}
	manager := jwt.NewManager("test-secret-for-jwt-testing", 3600, 2592000)
	s := &svc.ServiceContext{Repo: r, BotJWT: manager}
	issued, err := NewIssueBotTokenLogic(testCaller(t), s).IssueBotToken(&pb.IssueBotTokenReq{BotId: testBotID, UserId: testOwnerID})
	require.NoError(t, err)
	validated, err := NewValidateBotTokenLogic(t.Context(), s).ValidateBotToken(&pb.ValidateBotTokenReq{Token: issued.Token})
	require.NoError(t, err)
	assert.True(t, validated.Valid)
	assert.Equal(t, testBotID, validated.GetBotId())
	assert.Equal(t, testOwnerID, validated.GetOwnerId())
	r.bots[testBotID].Status = "disabled"
	validated, err = NewValidateBotTokenLogic(t.Context(), s).ValidateBotToken(&pb.ValidateBotTokenReq{Token: issued.Token})
	require.NoError(t, err)
	assert.False(t, validated.Valid)
	assert.Nil(t, validated.BotId)
}
func TestValidateBotToken_Normal(t *testing.T) {
	manager := jwt.NewManager("test-secret-for-jwt-testing", 3600, 2592000)
	token, err := manager.GenerateBotToken(testBotID, "platform", nil, "official")
	require.NoError(t, err)
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "platform", Type: "official", Status: "active"}
	logic := NewValidateBotTokenLogic(t.Context(), &svc.ServiceContext{Repo: r, BotJWT: manager})
	valid, err := logic.ValidateBotToken(&pb.ValidateBotTokenReq{Token: token})
	require.NoError(t, err)
	assert.True(t, valid.Valid)
	assert.Nil(t, valid.OwnerId)
	assert.Equal(t, "platform", valid.GetOwnerType())
	r.bots[testBotID].OwnerType = "user"
	r.bots[testBotID].OwnerID = stringPtr(testOwnerID)
	invalid, err := logic.ValidateBotToken(&pb.ValidateBotTokenReq{Token: token})
	require.NoError(t, err)
	assert.False(t, invalid.Valid)
}
func TestUpdateBot_Normal_SelfDeployed(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "self_deployed", ModelID: stringPtr(testModelID), MemoryModelID: stringPtr(testModelID), MemoryEmbeddingModelID: stringPtr(testModelID)}
	logic := NewUpdateBotLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	kept, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, Name: "renamed"})
	require.NoError(t, err)
	assert.Equal(t, testModelID, kept.GetModelId())
	cleared, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, ClearModelId: true})
	require.NoError(t, err)
	assert.Nil(t, cleared.ModelId)
	assert.Equal(t, testModelID, cleared.GetMemoryModelId())
	assert.Equal(t, testModelID, cleared.GetMemoryEmbeddingModelId())
	_, err = logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, ModelId: stringPtr(testModelID), ClearModelId: true})
	assert.ErrorIs(t, err, ErrBotInvalid)
	_, err = logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, ModelId: stringPtr(testPrivateModelID)})
	assert.ErrorIs(t, err, ErrBotForbidden)
	replaced, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, ModelId: stringPtr(testModelID), ClearMemoryModelId: true, ClearMemoryEmbeddingModelId: true})
	require.NoError(t, err)
	assert.Equal(t, testModelID, replaced.GetModelId())
	assert.Nil(t, replaced.MemoryModelId)
	assert.Nil(t, replaced.MemoryEmbeddingModelId)
}
func TestUpdateBot_Normal_ThirdParty(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "third_party", SubType: "ws", ConnMode: "ws"}
	updated, err := NewUpdateBotLogic(testCaller(t), &svc.ServiceContext{Repo: r}).UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, CallbackUrl: "https://new-cb.example.com", SubType: "webhook"})
	require.NoError(t, err)
	assert.Equal(t, "webhook", updated.ConnMode)
	assert.Equal(t, "https://new-cb.example.com", updated.CallbackUrl)
	assert.True(t, updated.HasWebhookSecret)
}
func TestUpdateBot_Forbidden(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "platform", Type: "official"}
	_, err := NewUpdateBotLogic(testCaller(t), &svc.ServiceContext{Repo: r}).UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID})
	assert.ErrorIs(t, err, ErrBotForbidden)
}
