package bot

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/metadata"
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
		if err.Error() == "webhook timestamp expired" {
			return nil, ErrWebhookTimeout
		}
		return nil, ErrWebhookInvalid
	}
	var payload struct {
		Type           string          `json:"type"`
		ConversationID string          `json:"conversation_id"`
		BindingID      string          `json:"binding_id"`
		RequesterID    *string         `json:"requester_id"`
		MsgType        string          `json:"msg_type"`
		Content        json.RawMessage `json:"content"`
		ReplyToID      *string         `json:"reply_to_id"`
		RawPayload     string          `json:"raw_payload"`
	}
	if err := json.Unmarshal(in.Body, &payload); err != nil {
		return nil, ErrBotInvalid
	}
	if err := model.ValidateEntityJSON(in.Body); err != nil {
		return nil, ErrBotInvalid
	}
	if payload.RawPayload != "" && model.ValidateEntityJSON([]byte(payload.RawPayload)) != nil {
		return nil, ErrBotInvalid
	}
	if payload.Type != "message.send" {
		return nil, ErrBotInvalid
	}
	if err := validateIDs(payload.ConversationID, payload.BindingID); err != nil {
		return nil, err
	}
	if payload.RequesterID != nil && validateIDs(*payload.RequesterID) != nil {
		return nil, ErrBotInvalid
	}
	if l.svcCtx.MessageClient == nil {
		return nil, ErrBotForbidden
	}
	req := &msgpb.SendBotReplyReq{BotId: bot.ID, ConversationId: payload.ConversationID, BindingId: payload.BindingID, RequesterId: payload.RequesterID, ReplyToId: payload.ReplyToID, RawPayload: payload.RawPayload}
	switch payload.MsgType {
	case "text":
		content := &msgpb.TextContent{}
		if protocol.Unmarshal(payload.Content, content) != nil {
			return nil, ErrBotInvalid
		}
		req.Type, req.Text = msgpb.MessageType_MESSAGE_TYPE_TEXT, content.Text
	case "file":
		req.File = &msgpb.FileContent{}
		if protocol.Unmarshal(payload.Content, req.File) != nil {
			return nil, ErrBotInvalid
		}
		req.Type = msgpb.MessageType_MESSAGE_TYPE_FILE
	case "result":
		req.Result = &msgpb.CustomContent{}
		if protocol.Unmarshal(payload.Content, req.Result) != nil {
			return nil, ErrBotInvalid
		}
		req.Type = msgpb.MessageType_MESSAGE_TYPE_RESULT
	default:
		return nil, ErrBotInvalid
	}
	md, _ := metadata.FromOutgoingContext(l.ctx)
	md = md.Copy()
	md.Delete("user-id")
	md.Delete("x-user-id")
	resp, err := l.svcCtx.MessageClient.SendBotReply(metadata.NewOutgoingContext(l.ctx, md), req)
	if err != nil {
		return nil, err
	}
	return &pb.WebhookResp{MessageId: &resp.MessageId}, nil
}

func (l *HandleIncomingWebhookLogic) resolveBot(in *pb.WebhookReq) (*botWithSecret, error) {
	if in.BotId != nil {
		if err := validateIDs(*in.BotId); err != nil {
			return nil, err
		}
		bot, err := l.svcCtx.Repo.GetBot(l.ctx, *in.BotId)
		if err != nil {
			return nil, ErrBotNotFound
		}
		if bot.Status != "active" || bot.Type != "third_party" || bot.SubType != SubTypeWebhook || bot.WebhookSecret == "" {
			return nil, ErrBotForbidden
		}
		return &botWithSecret{ID: bot.ID, WebhookSecret: bot.WebhookSecret}, nil
	}
	bots, err := l.svcCtx.Repo.ListActiveWebhookBots(l.ctx)
	if err != nil {
		return nil, ErrBotNotFound
	}
	for _, bot := range bots {
		if verifyWebhookSignature(in.Body, in.Signature, in.Timestamp, bot.WebhookSecret) == nil {
			return &botWithSecret{ID: bot.ID, WebhookSecret: bot.WebhookSecret}, nil
		}
	}
	return nil, ErrWebhookInvalid
}

type botWithSecret struct {
	ID            string
	WebhookSecret string
}
