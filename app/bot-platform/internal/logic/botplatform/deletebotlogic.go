package botplatform

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type DeleteBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteBotLogic {
	return &DeleteBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DeleteBotLogic) DeleteBot(in *pb.DeleteBotReq) (*common.BaseResponse, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		l.Errorf("get bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}

	if isOfficialInstance(bot) || !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	if err := l.svcCtx.Repo.DeleteBot(l.ctx, in.BotId); err != nil {
		l.Errorf("delete bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "delete bot failed", err)
	}

	l.Infof("bot deleted: bot_id=%d", in.BotId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

func isOfficialInstance(bot *model.Bot) bool {
	if bot == nil || bot.Settings == nil {
		return false
	}
	var settings map[string]any
	if err := json.Unmarshal(bot.Settings, &settings); err != nil {
		return false
	}
	v, _ := settings["official_instance"].(bool)
	return v
}
