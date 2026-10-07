package bot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	testBotID          = "019b0123-4567-789a-bcde-f01234567891"
	testOtherBotID     = "019b0123-4567-789a-bcde-f01234567892"
	testOwnerID        = "019b0123-4567-789a-bcde-f01234567893"
	testOtherOwnerID   = "019b0123-4567-789a-bcde-f01234567894"
	testModelID        = "019b0123-4567-789a-bcde-f01234567895"
	testPrivateModelID = "019b0123-4567-789a-bcde-f01234567896"
	testServerID       = "019b0123-4567-789a-bcde-f01234567897"
)

func stringPtr(value string) *string { return &value }
func testCaller(t *testing.T) context.Context {
	return context.WithValue(t.Context(), interceptor.ContextKeyUserID, testOwnerID)
}

func TestCreateBotValidation(t *testing.T) {
	r := newEnhancedMockRepo()
	logic := NewCreateBotLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	for _, request := range []*pb.CreateBotReq{
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Type: "self_deployed"},
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "invalid", Type: "invalid"},
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "webhook", Type: "third_party", SubType: "webhook"},
		{OwnerType: "user", Name: "no owner", Type: "self_deployed"},
		{OwnerType: "platform", Name: "elevated", Type: "self_deployed"},
		{OwnerType: "user", OwnerId: stringPtr(testOtherOwnerID), Name: "other owner", Type: "self_deployed"},
		{OwnerType: "user", OwnerId: stringPtr("10"), Name: "decimal owner", Type: "self_deployed"},
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "bad model", Type: "self_deployed", ModelId: stringPtr("0")},
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "other model", Type: "self_deployed", ModelId: stringPtr(testPrivateModelID)},
		{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "bad config", Type: "self_deployed", Settings: `{"model_id":10}`},
	} {
		_, err := logic.CreateBot(request)
		require.Error(t, err)
	}
	created, err := logic.CreateBot(&pb.CreateBotReq{OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "owned", Type: "self_deployed", ModelId: stringPtr(testModelID)})
	require.NoError(t, err)
	require.NoError(t, identity.Validate(created.Id))
	assert.Equal(t, "user", created.OwnerType)
	assert.Equal(t, testOwnerID, created.GetOwnerId())
	assert.Equal(t, testModelID, created.GetModelId())
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-secret-12345"
	message := []byte(`{"text":"hello world"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	signature := hex.EncodeToString(mac.Sum(nil))
	require.NoError(t, verifyWebhookSignature(message, signature, time.Now().Unix(), secret))
	assert.Error(t, verifyWebhookSignature(message, "bad-sig", time.Now().Unix(), secret))
	assert.Error(t, verifyWebhookSignature(message, signature, time.Now().Add(-10*time.Minute).Unix(), secret))
}

func TestCanWrite(t *testing.T) {
	bot := &model.Bot{OwnerType: "user", OwnerID: stringPtr(testOwnerID)}
	assert.True(t, canWrite(bot, testOwnerID))
	assert.False(t, canWrite(bot, testOtherOwnerID))
	assert.False(t, canWrite(&model.Bot{OwnerType: "platform"}, testOwnerID))
	assert.False(t, canWrite(&model.Bot{OwnerType: "user"}, testOwnerID))
}

func TestUpdateOfficialBotSavesStreamingEnabled(t *testing.T) {
	r := newEnhancedMockRepo()
	r.bots[testBotID] = &model.Bot{ID: testBotID, OwnerType: "user", OwnerID: stringPtr(testOwnerID), Type: "official", TemplateID: "qa", StreamingEnabled: true}
	logic := NewUpdateBotLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	updated, err := logic.UpdateBot(&pb.UpdateBotReq{BotId: testBotID, UserId: testOwnerID, StreamingEnabled: false})
	require.NoError(t, err)
	assert.False(t, updated.StreamingEnabled)
}

// Existing fake repository keeps behavior tests isolated from external services.
// Unused repository methods remain inaccessible rather than returning fake successes.
type officialInstanceRepo struct {
	repo.BotRepoInterface
	created *model.Bot
}

func (r *officialInstanceRepo) ResolveModelID(ctx context.Context, modelName, userID string, platform bool) (string, error) {
	if platform && modelName == "qwen-plus" {
		return testModelID, nil
	}
	return "", gorm.ErrRecordNotFound
}
func (r *officialInstanceRepo) GetModelOwner(ctx context.Context, modelID string) (string, *string, error) {
	if modelID == testModelID {
		return "platform", nil, nil
	}
	if modelID == testPrivateModelID {
		return "user", stringPtr(testOtherOwnerID), nil
	}
	return "", nil, gorm.ErrRecordNotFound
}
