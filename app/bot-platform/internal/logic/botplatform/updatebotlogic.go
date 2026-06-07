package botplatform

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
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
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		l.Errorf("get bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}

	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	// Note: UpdateBotReq has no Type field, so bot type is implicitly immutable via the API.

	updates := make(map[string]any)

	// All types can update name and avatar
	if in.Name != "" {
		updates["name"] = in.Name
	}
	if in.Avatar != "" {
		updates["avatar"] = in.Avatar
	}

	switch bot.Type {
	case TypeOfficial:
		updates["max_context_messages"] = in.MaxContextMessages
		updates["memory_limit"] = in.MemoryLimit
		// Official bots: only model and (for knowledge template) knowledge toggle
		if in.ModelName != "" {
			updates["model_name"] = in.ModelName
		}
		modelID := in.ModelId
		if in.ModelId <= 0 && in.ModelName != "" {
			if id, err := l.svcCtx.Repo.ResolveModelID(l.ctx, in.ModelName); err == nil && id > 0 {
				modelID = id
			}
		}
		if modelID > 0 {
			updates["model_id"] = modelID
			if ownerID, err := l.svcCtx.Repo.GetModelOwner(l.ctx, modelID); err == nil {
				updates["use_platform_model"] = (ownerID == 0)
			}
		}
		updates["streaming_enabled"] = in.StreamingEnabled
		if bot.TemplateID == TemplateKnowledge {
			updates["enable_knowledge"] = in.EnableKnowledge
		}

	case TypeSelfDeployed:
		// Self-deployed bots: full AI config
		if in.ModelName != "" {
			updates["model_name"] = in.ModelName
		}
		modelID := in.ModelId
		if in.ModelId <= 0 && in.ModelName != "" {
			if id, err := l.svcCtx.Repo.ResolveModelID(l.ctx, in.ModelName); err == nil && id > 0 {
				modelID = id
			}
		}
		if modelID > 0 {
			updates["model_id"] = modelID
			if ownerID, err := l.svcCtx.Repo.GetModelOwner(l.ctx, modelID); err == nil {
				updates["use_platform_model"] = (ownerID == 0)
			}
		}
		if in.SystemPrompt != "" {
			updates["system_prompt"] = in.SystemPrompt
		}
		if in.Persona != "" {
			updates["persona"] = in.Persona
		}
		updates["enable_knowledge"] = in.EnableKnowledge
		updates["temperature"] = in.Temperature
		updates["memory_limit"] = in.MemoryLimit
		updates["max_context_messages"] = in.MaxContextMessages
		updates["streaming_enabled"] = in.StreamingEnabled
		if in.MemoryModelName != "" {
			updates["memory_model_name"] = in.MemoryModelName
		}
		memoryModelID := in.MemoryModelId
		if in.MemoryModelId <= 0 && in.MemoryModelName != "" {
			if id, err := l.svcCtx.Repo.ResolveModelID(l.ctx, in.MemoryModelName); err == nil && id > 0 {
				memoryModelID = id
			}
		}
		if memoryModelID > 0 {
			updates["memory_model_id"] = memoryModelID
			if ownerID, err := l.svcCtx.Repo.GetModelOwner(l.ctx, memoryModelID); err == nil {
				updates["memory_use_platform_model"] = (ownerID == 0)
			}
		}

	case TypeThirdParty:
		// Third-party bots: connection config
		if in.SubType != "" {
			updates["sub_type"] = in.SubType
		}
		if in.ConnMode != "" {
			updates["conn_mode"] = NormalizeConnMode(in.ConnMode)
		}
		if in.CallbackUrl != "" {
			updates["callback_url"] = in.CallbackUrl
		}
	}

	if in.BotTags != nil {
		data, _ := json.Marshal(in.BotTags)
		updates["bot_tags"] = data
	}
	if len(in.ResponseTriggers) > 0 {
		data, _ := json.Marshal(in.ResponseTriggers)
		updates["response_triggers"] = data
	}
	if in.Capabilities != "" {
		updates["capabilities"] = []byte(in.Capabilities)
	}
	if in.Settings != "" {
		updates["settings"] = []byte(in.Settings)
	}

	if in.ApiKey != "" && len(l.svcCtx.EncryptionKey) > 0 {
		encrypted, encErr := crypto.EncryptString(in.ApiKey, l.svcCtx.EncryptionKey)
		if encErr != nil {
			l.Errorf("encrypt api key failed: %v", encErr)
			return nil, errors.Wrap(errors.CodeInternal, "encrypt api key failed", encErr)
		}
		updates["api_key_encrypted"] = encrypted
	}
	if in.MemoryApiKey != "" && len(l.svcCtx.EncryptionKey) > 0 {
		encrypted, encErr := crypto.EncryptString(in.MemoryApiKey, l.svcCtx.EncryptionKey)
		if encErr != nil {
			l.Errorf("encrypt memory api key failed: %v", encErr)
			return nil, errors.Wrap(errors.CodeInternal, "encrypt memory api key failed", encErr)
		}
		updates["memory_api_key_encrypted"] = encrypted
	}

	if len(updates) > 0 {
		if err := l.svcCtx.Repo.UpdateBot(l.ctx, in.BotId, updates); err != nil {
			l.Errorf("update bot failed: %v", err)
			return nil, errors.Wrap(errors.CodeDBError, "update bot failed", err)
		}
	}

	bot, err = l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("get bot after update failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}
	return modelBotToProto(bot), nil
}

func canWrite(bot *model.Bot, callerID int64) bool {
	if bot.OwnerID == 0 {
		return false // platform bots: only admin can modify (callerID check by middleware)
	}
	return callerID == bot.OwnerID
}
