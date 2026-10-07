package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
)

// Reply summaries share the caller's visibility overlay and never hydrate a
// foreign-conversation reference or reveal a recalled message's original body.
func hydrateReplySummaries(ctx context.Context, msgRepo *repo.MessageRepo, profiles *repo.ProfileRepo, convs *repo.ConversationRepo, pbMessages []*message.Message) {
	replyIDs := make(map[string]struct{})
	for _, msg := range pbMessages {
		if msg.ReplyToId != nil {
			replyIDs[*msg.ReplyToId] = struct{}{}
		}
	}
	if len(replyIDs) == 0 {
		return
	}
	ids := make([]string, 0, len(replyIDs))
	for id := range replyIDs {
		ids = append(ids, id)
	}
	replies, err := msgRepo.GetByIDs(ctx, ids)
	if err != nil {
		return
	}
	replyMap := make(map[string]model.Message, len(replies))
	var users, bots []string
	for _, reply := range replies {
		replyMap[reply.ID] = reply
		if reply.SenderID == nil {
			continue
		}
		switch reply.SenderType {
		case "bot":
			bots = append(bots, *reply.SenderID)
		case "user":
			users = append(users, *reply.SenderID)
		}
	}
	userNames, _ := profiles.UserNames(ctx, users)
	botNames := botNamesByID(ctx, convs, bots)
	for _, msg := range pbMessages {
		if msg.ReplyToId == nil {
			continue
		}
		reply, ok := replyMap[*msg.ReplyToId]
		if !ok || reply.ConvID != msg.ConversationId || reply.Status == model.MessageStatusRecalled {
			msg.ReplyTo = &message.ReplyMessageSummary{MessageId: *msg.ReplyToId, Deleted: true}
			continue
		}
		name := ""
		if reply.SenderID != nil {
			switch reply.SenderType {
			case "bot":
				name = botNames[*reply.SenderID]
				if name == "" {
					name = "Bot"
				}
			case "user":
				name = userNames[*reply.SenderID]
				if name == "" {
					name = "用户"
				}
			}
		}
		msg.ReplyTo = &message.ReplyMessageSummary{
			MessageId: reply.ID, SenderId: reply.SenderID, SenderType: reply.SenderType,
			SenderName: name, Type: message.MessageType(reply.MsgType),
			Preview: model.MessagePreview(reply.MsgType, reply.Content),
		}
	}
}

func resolveReplySenderName(ctx context.Context, svcCtx *svc.ServiceContext, senderID *string, senderType string) string {
	if senderID == nil {
		return ""
	}
	switch senderType {
	case "user":
		names, err := svcCtx.ProfileRepo.UserNames(ctx, []string{*senderID})
		if err == nil && names[*senderID] != "" {
			return names[*senderID]
		}
		return "用户"
	case "bot":
		if bot, err := svcCtx.ConversationRepo.GetBot(ctx, *senderID); err == nil && bot.Name != "" {
			return bot.Name
		}
		return "Bot"
	}
	return ""
}

func botNamesByID(ctx context.Context, convs *repo.ConversationRepo, botIDs []string) map[string]string {
	names := make(map[string]string, len(botIDs))
	if len(botIDs) == 0 || convs == nil {
		return names
	}
	bots, err := convs.GetBotsByIDs(ctx, botIDs)
	if err != nil {
		return names
	}
	for _, bot := range bots {
		names[bot.Id] = bot.Name
	}
	return names
}
