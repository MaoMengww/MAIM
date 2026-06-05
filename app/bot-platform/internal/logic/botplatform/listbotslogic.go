package botplatform

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type ListBotsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListBotsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListBotsLogic {
	return &ListBotsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListBotsLogic) ListBots(in *pb.ListBotsReq) (*pb.ListBotsResp, error) {
	if in.OwnerId > 0 {
		if err := l.ensureOfficialInstances(in.OwnerId); err != nil {
			return nil, err
		}
	}

	page := int32(1)
	pageSize := int32(20)
	if in.Pagination != nil {
		if in.Pagination.Page > 0 {
			page = in.Pagination.Page
		}
		if in.Pagination.PageSize > 0 {
			pageSize = in.Pagination.PageSize
			if pageSize > 100 {
				pageSize = 100
			}
		}
	}

	offset := int((page - 1) * pageSize)
	limit := int(pageSize)

	bots, total, err := l.svcCtx.Repo.ListBotsByOwner(l.ctx, in.OwnerId, in.Status, offset, limit)
	if err != nil {
		l.Errorf("list bots failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "list bots failed", err)
	}

	pbBots := make([]*pb.Bot, 0, len(bots))
	for i := range bots {
		pbBots = append(pbBots, modelBotToProto(&bots[i]))
	}

	totalPages := int32(0)
	if total > 0 {
		totalPages = int32((total + int64(pageSize) - 1) / int64(pageSize))
	}

	l.Infof("bots listed: creator=%d count=%d", in.OwnerId, len(pbBots))
	return &pb.ListBotsResp{
		Bots: pbBots,
		Pagination: &common.PaginationResp{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

func (l *ListBotsLogic) ensureOfficialInstances(ownerID int64) error {
	templates, err := l.svcCtx.Repo.ListOfficialTemplates(l.ctx)
	if err != nil {
		return errors.Wrap(errors.CodeDBError, "list official bot templates failed", err)
	}
	for i := range templates {
		tpl := &templates[i]
		if _, err := l.svcCtx.Repo.GetOfficialInstance(l.ctx, ownerID, tpl.ID); err == nil {
			continue
		} else if err != gorm.ErrRecordNotFound {
			return errors.Wrap(errors.CodeDBError, "get official bot instance failed", err)
		}
		if err := l.createOfficialInstance(ownerID, tpl); err != nil {
			return err
		}
	}
	return nil
}

func (l *ListBotsLogic) createOfficialInstance(ownerID int64, tpl *model.Bot) error {
	id, err := l.svcCtx.Repo.NextID(l.ctx)
	if err != nil {
		return errors.Wrap(errors.CodeInternal, "generate id failed", err)
	}
	settings := cloneJSONMap(tpl.Settings)
	settings["official_instance"] = true
	settings["official_template_id"] = strconv.FormatInt(tpl.ID, 10)
	if _, ok := settings["prompt_locale"]; !ok {
		settings["prompt_locale"] = "zh-CN"
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return errors.Wrap(errors.CodeInternal, "marshal bot settings failed", err)
	}

	bot := &model.Bot{
		ID:                     id,
		OwnerID:                ownerID,
		Name:                   tpl.Name,
		Avatar:                 tpl.Avatar,
		Type:                   tpl.Type,
		TemplateID:             tpl.TemplateID,
		Status:                 tpl.Status,
		UsePlatformModel:       tpl.UsePlatformModel,
		ModelName:              tpl.ModelName,
		BaseURL:                tpl.BaseURL,
		SystemPrompt:           tpl.SystemPrompt,
		Persona:                tpl.Persona,
		EnableKnowledge:        tpl.EnableKnowledge,
		Temperature:            tpl.Temperature,
		MaxContextMessages:     tpl.MaxContextMessages,
		StreamingEnabled:       tpl.StreamingEnabled,
		MemoryModelName:        tpl.MemoryModelName,
		MemoryUsePlatformModel: tpl.MemoryUsePlatformModel,
		ConnMode:               tpl.ConnMode,
		CallbackURL:            tpl.CallbackURL,
		BotTags:                append([]string{}, tpl.BotTags...),
		Settings:               settingsJSON,
	}
	if len(tpl.Capabilities) > 0 {
		bot.Capabilities = append([]byte{}, tpl.Capabilities...)
	}
	return l.svcCtx.Repo.CreateBot(l.ctx, bot)
}

func cloneJSONMap(data []byte) map[string]any {
	out := map[string]any{}
	if len(data) == 0 {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}
