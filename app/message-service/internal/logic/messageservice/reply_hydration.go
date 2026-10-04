package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
)

// hydrateReplySummaries populates the ReplyTo field on each proto message
// that has a non-zero ReplyToId.
func hydrateReplySummaries(ctx context.Context, msgRepo *repo.MessageRepo, profiles *repo.ProfileRepo, convs *repo.ConversationRepo, pbMessages []*message.Message) {
	// Collect distinct reply_to_msg_ids
	replyIDs := make(map[int64]struct{})
	for _, pbMsg := range pbMessages {
		if pbMsg.GetReplyToId() != 0 {
			replyIDs[pbMsg.GetReplyToId()] = struct{}{}
		}
	}
	if len(replyIDs) == 0 {
		return
	}

	ids := make([]int64, 0, len(replyIDs))
	for id := range replyIDs {
		ids = append(ids, id)
	}

	replies, err := msgRepo.GetByIDs(ctx, ids)
	if err != nil || len(replies) == 0 {
		return
	}

	// Build reply message map
	replyMap := make(map[int64]model.Message, len(replies))
	for i := range replies {
		replyMap[replies[i].ID] = replies[i]
	}

	// Resolve sender names
	type senderKey struct {
		id    int64
		stype string
	}
	senders := make(map[senderKey]struct{})
	for _, reply := range replies {
		stype := senderTypeFromMsgType(reply.MsgType)
		senders[senderKey{id: reply.SenderID, stype: stype}] = struct{}{}
	}

	var userIDs []int64
	var botIDs []int64
	for sk := range senders {
		switch sk.stype {
		case "user":
			userIDs = append(userIDs, sk.id)
		case "bot":
			botIDs = append(botIDs, sk.id)
		}
	}

	userNames := make(map[int64]string)
	if len(userIDs) > 0 {
		names, err := profiles.UserNames(ctx, userIDs)
		if err == nil {
			userNames = names
		}
	}

	botNames := botNamesByID(ctx, convs, botIDs)

	senderNames := make(map[int64]string, len(replies))
	for _, reply := range replies {
		stype := senderTypeFromMsgType(reply.MsgType)
		switch stype {
		case "user":
			if name, ok := userNames[reply.SenderID]; ok {
				senderNames[reply.SenderID] = name
			} else {
				senderNames[reply.SenderID] = "用户"
			}
		case "bot":
			if name, ok := botNames[reply.SenderID]; ok {
				senderNames[reply.SenderID] = name
			} else {
				senderNames[reply.SenderID] = "Bot"
			}
		}
	}

	// Set ReplyTo on each message
	for _, pbMsg := range pbMessages {
		replyID := pbMsg.GetReplyToId()
		if replyID == 0 {
			continue
		}
		reply, ok := replyMap[replyID]
		if !ok {
			continue
		}
		status := message.MessageStatus(reply.Status)
		deleted := status == message.MessageStatus_MESSAGE_STATUS_RECALLED
		stype := senderTypeFromMsgType(reply.MsgType)
		pbMsg.ReplyTo = &message.ReplyMessageSummary{
			MessageId:  reply.ID,
			SenderId:   reply.SenderID,
			SenderType: stype,
			SenderName: senderNames[reply.SenderID],
			Type:       message.MessageType(reply.MsgType),
			Preview:    extractTextPreview(reply.MsgType, reply.Content),
			Deleted:    deleted,
		}
	}
}

// resolveReplySenderName resolves a display name for a reply's sender.
func resolveReplySenderName(ctx context.Context, svcCtx *svc.ServiceContext, senderID int64, senderType string) string {
	switch senderType {
	case "user":
		names, err := svcCtx.ProfileRepo.UserNames(ctx, []int64{senderID})
		if err == nil && names[senderID] != "" {
			return names[senderID]
		}
		return "用户"
	case "bot":
		if bot, err := svcCtx.ConversationRepo.GetBot(ctx, senderID); err == nil && bot.Name != "" {
			return bot.Name
		}
		return "Bot"
	}
	return ""
}

// botNamesByID resolves bot display names from the bot domain's table (ADR-0007).
func botNamesByID(ctx context.Context, convs *repo.ConversationRepo, botIDs []int64) map[int64]string {
	names := make(map[int64]string, len(botIDs))
	if len(botIDs) == 0 || convs == nil {
		return names
	}
	bots, err := convs.GetBotsByIDs(ctx, botIDs)
	if err != nil {
		return names
	}
	for _, bot := range bots {
		names[bot.ID] = bot.Name
	}
	return names
}

func senderTypeFromMsgType(msgType int32) string {
	if msgType == model.MsgTypeBot {
		return "bot"
	}
	return "user"
}

// buildReplyToMap looks up a reply message and returns a map suitable for Kafka JSON.
func buildReplyToMap(ctx context.Context, svcCtx *svc.ServiceContext, replyToMsgID int64) map[string]any {
	replyMsg, err := svcCtx.MessageRepo.GetByID(ctx, replyToMsgID)
	if err != nil || replyMsg == nil {
		return map[string]any{
			"message_id": replyToMsgID,
			"deleted":    true,
		}
	}
	stype := senderTypeFromMsgType(replyMsg.MsgType)
	return map[string]any{
		"message_id":  replyMsg.ID,
		"sender_id":   replyMsg.SenderID,
		"sender_type": stype,
		"sender_name": resolveReplySenderName(ctx, svcCtx, replyMsg.SenderID, stype),
		"type":        replyMsg.MsgType,
		"preview":     extractTextPreview(replyMsg.MsgType, replyMsg.Content),
		"deleted":     replyMsg.Status == model.MessageStatusRecalled,
	}
}
