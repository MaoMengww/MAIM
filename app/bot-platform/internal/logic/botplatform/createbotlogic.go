package botplatform

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	"github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/zeromicro/go-zero/core/logx"
)

type CreateBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCreateBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateBotLogic {
	return &CreateBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *CreateBotLogic) CreateBot(in *botplatform.CreateBotReq) (*botplatform.Bot, error) {
	if l.svcCtx.Repo == nil {
		return nil, fmt.Errorf("repo not initialized")
	}

	if in.Name == "" {
		return nil, fmt.Errorf("bot name is required")
	}

	botType := NormalizeBotType(in.Type)
	switch botType {
	case consts.BotTypeOfficial, consts.BotTypeSelfDeployed, consts.BotTypeThirdParty:
	default:
		return nil, fmt.Errorf("invalid bot type: %s", in.Type)
	}

	if botType == consts.BotTypeSelfDeployed && in.OwnerId == 0 {
		return nil, fmt.Errorf("owner_id is required for user bot")
	}

	// capture normalized subType for third-party bot construction below
	var subType string
	if botType == consts.BotTypeThirdParty {
		subType = in.SubType
		if subType == "" {
			// fall back to conn_mode for backwards compatibility
			subType = NormalizeConnMode(in.ConnMode)
		}
		if subType != SubTypeWS && subType != SubTypeWebhook {
			return nil, fmt.Errorf("invalid sub_type: %s, must be 'ws' or 'webhook'", subType)
		}
		if subType == SubTypeWebhook && in.CallbackUrl == "" {
			return nil, fmt.Errorf("callback_url is required for webhook mode")
		}
	}

	// Official bot: require template_id
	if botType == consts.BotTypeOfficial {
		if in.TemplateId == "" {
			return nil, fmt.Errorf("template_id is required for official bot")
		}
		if in.TemplateId != TemplateQA && in.TemplateId != TemplateKnowledge {
			return nil, fmt.Errorf("invalid template_id: %s, must be 'qa' or 'knowledge'", in.TemplateId)
		}
	}

	botID, err := l.svcCtx.Repo.NextID(l.ctx)
	if err != nil {
		return nil, fmt.Errorf("generate bot id failed: %w", err)
	}

	var webhookSecret string
	if botType == consts.BotTypeThirdParty && subType == SubTypeWebhook {
		s, genErr := generateRandomSecret(32)
		if genErr != nil {
			return nil, fmt.Errorf("generate webhook secret: %w", genErr)
		}
		webhookSecret = s
	}

	bot := &model.Bot{
		ID:                       botID,
		OwnerID:                  in.OwnerId,
		Name:                     in.Name,
		Avatar:                   in.Avatar,
		Type:                     botType,
		Status:                   consts.BotStatusActive,
		UsePlatformModel:         in.UsePlatformModel,
		ModelName:                in.ModelName,
		ModelID:                  in.ModelId,
		BaseURL:                  in.BaseUrl,
		SystemPrompt:             in.SystemPrompt,
		Persona:                  in.Persona,
		EnableKnowledge:          in.EnableKnowledge,
		Temperature:              in.Temperature,
		MaxContextMessages:       int(in.MaxContextMessages),
		MaxContextTokens:         int(in.MaxContextTokens),
		StreamingEnabled:         in.StreamingEnabled,
		MemoryModelName:          in.MemoryModelName,
		MemoryModelID:            in.MemoryModelId,
		MemoryUsePlatformModel:   in.MemoryUsePlatformModel,
		MemoryLimit:              int(in.MemoryLimit),
		MemoryEmbeddingModelName: in.MemoryEmbeddingModelName,
		MemoryEmbeddingModelID:   in.MemoryEmbeddingModelId,
		ConnMode:                 NormalizeConnMode(in.ConnMode),
		CallbackURL:              in.CallbackUrl,
		WebhookSecret:            webhookSecret,
		SubType:                  subType,
		TemplateID:               in.TemplateId,
		BotTags:                  in.BotTags,
		ResponseTriggers:         in.ResponseTriggers,
	}

	if len(bot.ResponseTriggers) == 0 {
		bot.ResponseTriggers = []string{"mention"}
	}

	if in.Capabilities != "" {
		bot.Capabilities = []byte(in.Capabilities)
	}
	if in.Settings != "" {
		bot.Settings = []byte(in.Settings)
	}

	if in.ApiKey != "" && len(l.svcCtx.EncryptionKey) > 0 {
		encrypted, err := crypto.EncryptString(in.ApiKey, l.svcCtx.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt api key failed: %w", err)
		}
		bot.APIKeyEncrypted = encrypted
	}

	if in.MemoryApiKey != "" && len(l.svcCtx.EncryptionKey) > 0 {
		encrypted, err := crypto.EncryptString(in.MemoryApiKey, l.svcCtx.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt memory api key failed: %w", err)
		}
		bot.MemoryAPIKeyEncrypted = encrypted
	}

	if bot.ModelID <= 0 && bot.ModelName != "" {
		resolvedID, err := l.svcCtx.Repo.ResolveModelID(l.ctx, bot.ModelName)
		if err == nil && resolvedID > 0 {
			bot.ModelID = resolvedID
		}
	}

	if bot.MemoryModelID <= 0 && bot.MemoryModelName != "" {
		resolvedID, err := l.svcCtx.Repo.ResolveModelID(l.ctx, bot.MemoryModelName)
		if err == nil && resolvedID > 0 {
			bot.MemoryModelID = resolvedID
		}
	}
	if bot.MemoryEmbeddingModelID <= 0 && bot.MemoryEmbeddingModelName != "" {
		resolvedID, err := l.svcCtx.Repo.ResolveModelID(l.ctx, bot.MemoryEmbeddingModelName)
		if err == nil && resolvedID > 0 {
			bot.MemoryEmbeddingModelID = resolvedID
		}
	}

	// Auto-determine use_platform_model from model owner
	if bot.ModelID > 0 {
		if ownerID, err := l.svcCtx.Repo.GetModelOwner(l.ctx, bot.ModelID); err == nil {
			bot.UsePlatformModel = (ownerID == 0)
		}
	} else {
		bot.UsePlatformModel = false
	}
	if bot.MemoryModelID > 0 {
		if ownerID, err := l.svcCtx.Repo.GetModelOwner(l.ctx, bot.MemoryModelID); err == nil {
			bot.MemoryUsePlatformModel = (ownerID == 0)
		}
	} else {
		bot.MemoryUsePlatformModel = false
	}

	if err := l.svcCtx.Repo.CreateBot(l.ctx, bot); err != nil {
		return nil, fmt.Errorf("create bot failed: %w", err)
	}

	l.Infof("bot created: bot_id=%d name=%s", botID, in.Name)
	return modelBotToProto(bot), nil
}
