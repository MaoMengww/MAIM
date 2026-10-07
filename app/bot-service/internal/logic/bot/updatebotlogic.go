package bot

import (
	"context"
	"encoding/json"
	stderrors "errors"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type UpdateBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateBotLogic {
	return &UpdateBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateBotLogic) UpdateBot(in *pb.UpdateBotReq) (*pb.Bot, error) {
	if err := validateCaller(l.ctx, in.UserId, in.BotId); err != nil {
		return nil, err
	}
	for _, ref := range []struct {
		id    *string
		clear bool
		name  string
	}{
		{in.ModelId, in.ClearModelId, in.ModelName},
		{in.MemoryModelId, in.ClearMemoryModelId, in.MemoryModelName},
		{in.MemoryEmbeddingModelId, in.ClearMemoryEmbeddingModelId, in.MemoryEmbeddingModelName},
	} {
		if err := validateReferenceUpdate(ref.id, ref.clear, ref.name); err != nil {
			return nil, err
		}
	}
	for _, data := range []string{in.Capabilities, in.Settings} {
		if data != "" && model.ValidateEntityJSON([]byte(data)) != nil {
			return nil, ErrBotInvalid
		}
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBotNotFound
		}
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}
	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}
	updates := make(map[string]any)
	if in.Name != "" {
		updates["name"] = in.Name
	}
	if in.Avatar != "" {
		updates["avatar"] = in.Avatar
	}
	if in.Status != "" {
		if in.Status != "active" && in.Status != "disabled" {
			return nil, ErrBotInvalid
		}
		updates["status"] = in.Status
	}

	switch bot.Type {
	case consts.BotTypeOfficial, consts.BotTypeSelfDeployed:
		if err := l.updateModelReference(updates, "model_id", "model_name", "use_platform_model", in.ModelId, in.ClearModelId, in.ModelName, in.UserId, in.UsePlatformModel); err != nil {
			return nil, err
		}
		if err := l.updateModelReference(updates, "memory_model_id", "memory_model_name", "memory_use_platform_model", in.MemoryModelId, in.ClearMemoryModelId, in.MemoryModelName, in.UserId, in.MemoryUsePlatformModel); err != nil {
			return nil, err
		}
		if err := l.updateModelReference(updates, "memory_embedding_model_id", "memory_embedding_model_name", "", in.MemoryEmbeddingModelId, in.ClearMemoryEmbeddingModelId, in.MemoryEmbeddingModelName, in.UserId, in.MemoryUsePlatformModel); err != nil {
			return nil, err
		}
		updates["max_context_messages"] = in.MaxContextMessages
		updates["max_context_tokens"] = in.MaxContextTokens
		updates["memory_limit"] = in.MemoryLimit
		updates["streaming_enabled"] = in.StreamingEnabled
		if bot.Type == consts.BotTypeSelfDeployed {
			if in.BaseUrl != "" {
				updates["base_url"] = in.BaseUrl
			}
			if in.SystemPrompt != "" {
				updates["system_prompt"] = in.SystemPrompt
			}
			if in.Persona != "" {
				updates["persona"] = in.Persona
			}
			updates["temperature"] = in.Temperature
			updates["enable_knowledge"] = in.EnableKnowledge
		} else if bot.TemplateID == TemplateKnowledge {
			updates["enable_knowledge"] = in.EnableKnowledge
		}
	case consts.BotTypeThirdParty:
		subType := bot.SubType
		if in.SubType != "" {
			subType = in.SubType
		}
		if in.ConnMode != "" {
			mode := NormalizeConnMode(in.ConnMode)
			if in.SubType != "" && subType != mode {
				return nil, ErrBotInvalid
			}
			subType = mode
		}
		if subType != SubTypeWS && subType != SubTypeWebhook {
			return nil, ErrBotInvalid
		}
		updates["sub_type"] = subType
		updates["conn_mode"] = subType
		callbackURL := bot.CallbackURL
		if in.CallbackUrl != "" {
			callbackURL = in.CallbackUrl
			updates["callback_url"] = callbackURL
		}
		if subType == SubTypeWebhook {
			if callbackURL == "" {
				return nil, ErrBotInvalid
			}
			if bot.WebhookSecret == "" {
				secret, err := generateRandomSecret(32)
				if err != nil {
					return nil, err
				}
				updates["webhook_secret"] = secret
			}
		}
	}
	if in.BotTags != nil {
		data, _ := json.Marshal(in.BotTags)
		updates["bot_tags"] = data
	}
	if in.ResponseTriggers != nil {
		data, _ := json.Marshal(in.ResponseTriggers)
		updates["response_triggers"] = data
	}
	if in.Capabilities != "" {
		updates["capabilities"] = []byte(in.Capabilities)
	}
	if in.Settings != "" {
		settings := cloneJSONMap([]byte(in.Settings))
		previous := cloneJSONMap(bot.Settings)
		// Template provenance is server-owned and must not be removed or forged.
		for _, key := range []string{"official_instance", "official_template_id"} {
			if value, ok := previous[key]; ok {
				settings[key] = value
			} else {
				delete(settings, key)
			}
		}
		data, err := json.Marshal(settings)
		if err != nil {
			return nil, ErrBotInvalid
		}
		updates["settings"] = data
	}
	for _, key := range []struct{ input, column string }{
		{in.ApiKey, "api_key_encrypted"}, {in.MemoryApiKey, "memory_api_key_encrypted"},
	} {
		if key.input == "" {
			continue
		}
		encrypted, err := crypto.EncryptString(key.input, l.svcCtx.EncryptionKey)
		if err != nil {
			return nil, errors.Wrap(errors.CodeInternal, "encrypt api key failed", err)
		}
		updates[key.column] = encrypted
	}
	if err := l.svcCtx.Repo.UpdateBot(l.ctx, in.BotId, updates); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update bot failed", err)
	}
	bot, err = l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}
	return modelBotToProto(bot), nil
}

func (l *UpdateBotLogic) updateModelReference(updates map[string]any, idColumn, nameColumn, platformColumn string, id *string, clear bool, name, userID string, platform bool) error {
	if clear {
		updates[idColumn] = nil
		updates[nameColumn] = ""
		if platformColumn != "" {
			updates[platformColumn] = false
		}
		return nil
	}
	if id == nil && name == "" {
		return nil
	}
	selected, usePlatform, err := selectModel(l.ctx, l.svcCtx.Repo, id, name, userID, platform)
	if err != nil {
		return err
	}
	updates[idColumn] = selected
	// A replacement UUID without a name must not retain an unrelated routing name.
	updates[nameColumn] = name
	if platformColumn != "" {
		updates[platformColumn] = usePlatform
	}
	return nil
}
