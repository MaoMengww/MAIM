package repo

import (
	"context"
	"fmt"

	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	convpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"github.com/maomeng/aim/pkg/consts"
)

type MemberRepo struct {
	convClient  convpb.ConversationServiceClient
	botPlatform botplatform.BotPlatformClient
}

func NewMemberRepo(convClient convpb.ConversationServiceClient, botPlatform botplatform.BotPlatformClient) *MemberRepo {
	return &MemberRepo{convClient: convClient, botPlatform: botPlatform}
}

func (r *MemberRepo) GetConvMembers(ctx context.Context, convID int64) ([]int64, error) {
	resp, err := r.convClient.GetMembers(ctx, &convpb.GetMembersReq{ConversationId: convID})
	if err != nil {
		return nil, fmt.Errorf("get members via gRPC: %w", err)
	}
	ids := make([]int64, len(resp.Members))
	for i, m := range resp.Members {
		ids[i] = m.UserId
	}
	return ids, nil
}

func (r *MemberRepo) GetConvBotsWithConfig(ctx context.Context, convID int64) ([]model.BotInfo, error) {
	resp, err := r.convClient.ListBots(ctx, &convpb.ListBotsReq{ConversationId: convID})
	if err != nil {
		return nil, fmt.Errorf("list bots via gRPC: %w", err)
	}
	bots := make([]model.BotInfo, 0, len(resp.Bots))
	for _, b := range resp.Bots {
		webhookSecret := ""
		connMode := ""
		callbackURL := ""
		botType := consts.BotTypeOfficial
		if r.botPlatform != nil {
			wc, wcErr := r.botPlatform.GetBotWebhookConfig(ctx, &botplatform.GetBotWebhookConfigReq{BotId: b.BotId})
			if wcErr == nil {
				connMode = wc.ConnMode
				callbackURL = wc.CallbackUrl
				webhookSecret = wc.WebhookSecret
				botType = wc.Type
			}
		}
		bots = append(bots, model.BotInfo{
			BotID: b.BotId, BotType: botType, ConnMode: connMode,
			CallbackURL: callbackURL, WebhookSecret: webhookSecret, ConvID: convID, Status: consts.BotStatusActive,
		})
	}
	return bots, nil
}
