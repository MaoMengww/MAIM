package botplatform

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/zeromicro/go-zero/core/logx"
)

type HandleIncomingWebhookLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewHandleIncomingWebhookLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HandleIncomingWebhookLogic {
	return &HandleIncomingWebhookLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *HandleIncomingWebhookLogic) HandleIncomingWebhook(in *pb.WebhookReq) (*pb.WebhookResp, error) {
	bot, err := l.resolveBot(in)
	if err != nil {
		return nil, err
	}

	if err := verifyWebhookSignature(in.Body, in.Signature, in.Timestamp, bot.WebhookSecret); err != nil {
		l.Errorf("webhook signature invalid: bot_id=%d err=%v", bot.ID, err)
		if err.Error() == "webhook timestamp expired" {
			return nil, ErrWebhookTimeout
		}
		return nil, ErrWebhookInvalid
	}

	var payload struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		Text           string `json:"text"`
		ReplyToID      string `json:"reply_to_id"`
		RawPayload     string `json:"raw_payload"`
	}
	if err := json.Unmarshal(in.Body, &payload); err != nil {
		l.Errorf("webhook body parse failed: bot_id=%d err=%v", bot.ID, err)
		return nil, err
	}

	convID, _ := strconv.ParseInt(payload.ConversationID, 10, 64)
	replyToID, _ := strconv.ParseInt(payload.ReplyToID, 10, 64)

	var messageID int64
	switch payload.Type {
	case "message.send":
		if l.svcCtx.MessageClient == nil {
			l.Errorf("message service unavailable: bot_id=%d", bot.ID)
			return nil, ErrBotForbidden
		}
		req := &msgpb.SendBotReplyReq{
			BotId:          bot.ID,
			ConversationId: convID,
			Text:           payload.Text,
			RawPayload:     payload.RawPayload,
		}
		if replyToID > 0 {
			req.ReplyToId = &replyToID
		}
		resp, err := l.svcCtx.MessageClient.SendBotReply(l.ctx, req)
		if err != nil {
			l.Errorf("send bot reply failed: bot_id=%d conv_id=%d err=%v", bot.ID, convID, err)
			return nil, err
		}
		messageID = resp.MessageId

	default:
		l.Infof("webhook unknown type: bot_id=%d type=%s", bot.ID, payload.Type)
	}

	l.Infof("webhook processed: bot_id=%d type=%s message_id=%d", bot.ID, payload.Type, messageID)
	return &pb.WebhookResp{MessageId: messageID}, nil
}

func (l *HandleIncomingWebhookLogic) resolveBot(in *pb.WebhookReq) (*botWithSecret, error) {
	if in.BotId > 0 {
		bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
		if err != nil {
			l.Errorf("webhook bot not found: bot_id=%d err=%v", in.BotId, err)
			return nil, ErrBotNotFound
		}
		if bot.Status != "active" {
			return nil, ErrBotForbidden
		}
		return &botWithSecret{ID: bot.ID, WebhookSecret: bot.WebhookSecret}, nil
	}

	bots, err := l.svcCtx.Repo.ListActiveWebhookBots(l.ctx)
	if err != nil {
		l.Errorf("list active webhook bots failed: %v", err)
		return nil, ErrBotNotFound
	}

	for _, b := range bots {
		if verifyWebhookSignature(in.Body, in.Signature, in.Timestamp, b.WebhookSecret) == nil {
			return &botWithSecret{ID: b.ID, WebhookSecret: b.WebhookSecret}, nil
		}
	}

	l.Errorf("no webhook bot matched the signature")
	return nil, ErrWebhookInvalid
}

type botWithSecret struct {
	ID            int64
	WebhookSecret string
}
