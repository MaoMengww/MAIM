package bot

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/zeromicro/go-zero/core/logx"
)

type ValidateBotTokenLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewValidateBotTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ValidateBotTokenLogic {
	return &ValidateBotTokenLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ValidateBotTokenLogic) ValidateBotToken(in *pb.ValidateBotTokenReq) (*pb.ValidateBotTokenResp, error) {
	return l.validateToken(in, false)
}

// ValidateExecutorStopToken authenticates an existing executor without granting
// any active Bot/resource privilege. It is used only for material-free outcomes.
func (l *ValidateBotTokenLogic) ValidateExecutorStopToken(in *pb.ValidateBotTokenReq) (*pb.ValidateBotTokenResp, error) {
	return l.validateToken(in, true)
}

func (l *ValidateBotTokenLogic) validateToken(in *pb.ValidateBotTokenReq, stopConfirmation bool) (*pb.ValidateBotTokenResp, error) {
	claims, err := l.svcCtx.BotJWT.ParseBotToken(in.Token)
	if err != nil {
		l.Infof("bot token parse failed: err=%v", err)
		return &pb.ValidateBotTokenResp{Valid: false}, nil
	}

	bot, err := l.svcCtx.Repo.GetBot(l.ctx, claims.BotID)
	if err != nil || (!stopConfirmation && bot.Status != "active") || bot.OwnerType != claims.OwnerType || bot.Type != claims.Type || !sameOwner(bot.OwnerID, claims.OwnerID) {
		return &pb.ValidateBotTokenResp{Valid: false}, nil
	}
	if stopConfirmation {
		var issued int64
		if l.svcCtx.DB == nil {
			return &pb.ValidateBotTokenResp{Valid: false}, nil
		}
		if err := l.svcCtx.DB.WithContext(l.ctx).Table("bot.issued_tokens").Where("token_id = ? AND bot_id = ?", claims.ID, bot.ID).Count(&issued).Error; err != nil {
			return nil, err
		}
		if issued != 1 {
			return &pb.ValidateBotTokenResp{Valid: false}, nil
		}
	}
	if !stopConfirmation && bot.CredentialsRevokedAt != nil {
		if l.svcCtx.DB == nil {
			return &pb.ValidateBotTokenResp{Valid: false}, nil
		}
		var count int64
		if err := l.svcCtx.DB.WithContext(l.ctx).Table("bot.issued_tokens").Where("token_id = ? AND bot_id = ? AND issued_at > ?", claims.ID, bot.ID, *bot.CredentialsRevokedAt).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 1 {
			return &pb.ValidateBotTokenResp{Valid: false}, nil
		}
	}

	return &pb.ValidateBotTokenResp{
		Valid:     true,
		BotId:     &claims.BotID,
		OwnerId:   claims.OwnerID,
		Type:      claims.Type,
		OwnerType: &claims.OwnerType,
	}, nil
}

func sameOwner(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
