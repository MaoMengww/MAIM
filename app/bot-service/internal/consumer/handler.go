package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/IBM/sarama"
	"slices"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/run"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Handler struct {
	bindings *repo.ConvBotRepo
	bots     *repo.BotRepo
	runs     *run.Service
}

func NewHandler(bots *repo.BotRepo, bindings *repo.ConvBotRepo, runs *run.Service) *Handler {
	return &Handler{bindings: bindings, bots: bots, runs: runs}
}

func (h *Handler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *Handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *Handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for message := range claim.Messages() {
		for {
			if err := h.Handle(session.Context(), message.Value); err == nil {
				break
			} else {
				logx.Errorf("Bot input handling retry: topic=%s partition=%d offset=%d code=%s", message.Topic, message.Partition, message.Offset, status.Code(err))
			}
			select {
			case <-session.Context().Done():
				return session.Context().Err()
			case <-time.After(time.Second):
			}
		}
		session.MarkMessage(message, "")
	}
	return nil
}
func (h *Handler) Handle(ctx context.Context, raw []byte) error {
	var envelope struct {
		Kind       string `json:"kind"`
		SenderType string `json:"sender_type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	if envelope.Kind != "" && envelope.Kind != "message.new" {
		return nil
	}
	if envelope.SenderType == "bot" || envelope.SenderType == "system" {
		return nil
	}
	event, _, err := parseEvent(raw)
	if err != nil {
		return nil
	}
	if event.Sender == nil || event.Sender.SenderType != "user" || event.Sender.UserID == nil || event.Message == nil {
		return nil
	}
	if event.EventType != "" && event.EventType != "message.created" {
		return nil
	}
	bindings, err := h.bindings.FindByConv(ctx, event.ConvID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if event.BotID != "" && event.BotID != binding.BotID {
			continue
		}
		if event.BindingID != "" && event.BindingID != binding.ID {
			continue
		}
		if event.Message.Seq <= binding.StartSeq {
			continue
		}
		bot, err := h.bots.GetBot(ctx, binding.BotID)
		// A deleted Bot can leave a stale messaging binding. It has no authority
		// to observe or accept work, and must not poison this Kafka partition.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if bot.Status != "active" {
			continue
		}
		source := event.Message.MsgID
		input := run.Input{BotID: binding.BotID, ConvID: event.ConvID, BindingID: binding.ID, RequesterID: *event.Sender.UserID, SubmissionKey: "message:" + source, Message: event.Message.Text, SourceMessageID: &source, ReplyToMsgID: event.Message.ReplyToMsgID, Language: event.Sender.Language}
		observationErr := h.runs.Observe(ctx, input)
		switch status.Code(observationErr) {
		case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition:
			observationErr = nil
		}
		// Observation transport failure never authorizes or prevents accepting work.
		// On a transport retry, Accept retains the message submission identity.
		replied, replyErr := h.runs.ReplyInput(ctx, input)
		if replyErr != nil {
			switch status.Code(replyErr) {
			case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition, codes.AlreadyExists:
			default:
				return replyErr
			}
		}
		if !replied && shouldRespond(event, bot) {
			_, err = h.runs.Accept(ctx, input)
			if err != nil {
				switch status.Code(err) {
				case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition, codes.AlreadyExists:
				default:
					return err
				}
			}
		}
		if observationErr != nil {
			return observationErr
		}
	}
	return nil
}

// parseEvent parses a Kafka message.created event, handling both nested (BotEvent)
// and flat formats.
func parseEvent(raw []byte) (*model.BotEvent, string, error) {
	var evt model.BotEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nil, "", err
	}
	if evt.Message == nil {
		var flat struct {
			MessageID    string  `json:"message_id"`
			SenderID     *string `json:"sender_id"`
			SenderName   string  `json:"sender_name"`
			SenderType   string  `json:"sender_type"`
			MsgType      int32   `json:"msg_type"`
			CreatedAt    int64   `json:"created_at"`
			Seq          int64   `json:"seq"`
			ReplyToMsgID *string `json:"reply_to_msg_id"`
			Content      struct {
				Text     string   `json:"text"`
				Mentions []string `json:"mention_user_ids"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &flat); err != nil {
			return nil, "", err
		}
		if evt.EventType == "" {
			evt.EventType = "message.created"
		}
		evt.Message = &model.EventMessage{MsgID: flat.MessageID, Text: flat.Content.Text, MsgType: flat.MsgType, ReplyToMsgID: flat.ReplyToMsgID, CreatedAt: flat.CreatedAt, Seq: flat.Seq}
		evt.Sender = &model.EventSender{UserID: flat.SenderID, SenderType: flat.SenderType, Username: flat.SenderName}
		evt.MentionedUserIDs = flat.Content.Mentions
	}
	if identity.Validate(evt.ConvID) != nil || identity.Validate(evt.Message.MsgID) != nil || (evt.BotID != "" && identity.Validate(evt.BotID) != nil) {
		return nil, "", fmt.Errorf("invalid bot event identity")
	}
	if (evt.BindingID != "" && identity.Validate(evt.BindingID) != nil) || (evt.HistoryGrantID != "" && identity.Validate(evt.HistoryGrantID) != nil) {
		return nil, "", fmt.Errorf("invalid bot binding or history grant identity")
	}
	if evt.Sender != nil && evt.Sender.UserID != nil && identity.Validate(*evt.Sender.UserID) != nil {
		return nil, "", fmt.Errorf("invalid event sender identity")
	}
	if evt.Message.ReplyToMsgID != nil && identity.Validate(*evt.Message.ReplyToMsgID) != nil {
		return nil, "", fmt.Errorf("invalid reply identity")
	}
	for _, id := range evt.MentionedUserIDs {
		if identity.Validate(id) != nil {
			return nil, "", fmt.Errorf("invalid mention identity")
		}
	}
	return &evt, evt.Message.MsgID, nil
}

// Only a mention of this Bot accepts work; merely observing activity
// or arbitrary mentions, keywords and Bot replies cannot authorize execution.
func shouldRespond(event *model.BotEvent, bot *model.Bot) bool {
	return bot != nil && event != nil && event.Message != nil && slices.Contains(event.MentionedUserIDs, bot.ID)
}
