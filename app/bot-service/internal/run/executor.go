package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	msg "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type executorUpdate struct {
	Type          string          `json:"type"`
	RunID         string          `json:"run_id"`
	Generation    int64           `json:"generation"`
	InputVersion  int64           `json:"input_version"`
	Sequence      int64           `json:"sequence"`
	UpdateKey     string          `json:"update_key"`
	Operation     string          `json:"operation,omitempty"`
	Status        string          `json:"status"`
	Stage         string          `json:"stage"`
	ResultText    string          `json:"result_text"`
	WaitingReason string          `json:"waiting_reason,omitempty"`
	MsgType       string          `json:"msg_type"`
	Content       json.RawMessage `json:"content"`
}

func decodeUpdate(body []byte) (executorUpdate, error) {
	var update executorUpdate
	if len(body) > 524288 {
		return update, status.Error(codes.InvalidArgument, "executor update too large")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&update); err != nil {
		return update, status.Error(codes.InvalidArgument, "invalid executor update")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return update, status.Error(codes.InvalidArgument, "trailing update content")
	}
	if identity.Validate(update.RunID) != nil || update.Generation < 1 || update.InputVersion < 1 || update.Sequence < 0 || update.UpdateKey == "" || len(update.UpdateKey) > 256 || len(update.Stage) > 128 || len(update.ResultText) > 262144 {
		return update, status.Error(codes.InvalidArgument, "invalid executor update fields")
	}
	if len(update.WaitingReason) > 2048 || (update.WaitingReason != "" && update.Status != "waiting_input") {
		return update, status.Error(codes.InvalidArgument, "waiting reason requires waiting_input")
	}
	if update.Type != "run.delivery_ack" && update.Operation != "" {
		return update, status.Error(codes.InvalidArgument, "operation belongs only to delivery acknowledgment")
	}
	switch update.Type {
	case "run.delivery_ack":
		if update.Sequence != 0 || update.Status != "" || update.Stage != "" || update.ResultText != "" || update.MsgType != "" || len(update.Content) > 0 {
			return update, status.Error(codes.InvalidArgument, "delivery ACK contains business update")
		}
	case "run.claim":
		if update.Sequence != 0 || update.Status != "accepted" || update.Stage != "" || update.ResultText != "" || update.MsgType != "" || len(update.Content) > 0 {
			return update, status.Error(codes.InvalidArgument, "invalid claim")
		}
	case "run.update":
		if update.Sequence < 1 {
			return update, status.Error(codes.InvalidArgument, "updates require positive sequence")
		}
		switch update.Status {
		case "running", "waiting_input", "stopped", "failed", "completed":
		default:
			return update, status.Error(codes.InvalidArgument, "invalid executor status")
		}
		if update.Status != "completed" && (update.ResultText != "" || update.MsgType != "" || len(update.Content) > 0) {
			return update, status.Error(codes.InvalidArgument, "result requires completion")
		}
		if update.Status == "completed" {
			if err := normalizeResult(&update); err != nil {
				return update, err
			}
		}
	default:
		return update, status.Error(codes.InvalidArgument, "unknown executor update type")
	}
	return update, nil
}

func normalizeResult(update *executorUpdate) error {
	if update.MsgType == "" {
		update.MsgType = "text"
	}
	switch update.MsgType {
	case "text":
		content := &msg.TextContent{}
		if len(update.Content) > 0 {
			if err := protocol.Unmarshal(update.Content, content); err != nil {
				return status.Error(codes.InvalidArgument, "invalid text result")
			}
		}
		if content.Text == "" {
			content.Text = update.ResultText
		}
		if strings.TrimSpace(content.Text) == "" || len(content.Text) > 262144 {
			return status.Error(codes.InvalidArgument, "empty or oversized result")
		}
		if update.ResultText != "" && update.ResultText != content.Text {
			return status.Error(codes.InvalidArgument, "result text differs from content")
		}
		update.ResultText = content.Text
		raw, err := protocol.Marshal(content)
		if err != nil {
			return err
		}
		update.Content = raw
	case "file":
		content := &msg.FileContent{}
		if err := protocol.Unmarshal(update.Content, content); err != nil {
			return status.Error(codes.InvalidArgument, "invalid file result")
		}
		if identity.Validate(content.FileId) != nil {
			return status.Error(codes.InvalidArgument, "invalid file identity")
		}
		raw, err := protocol.Marshal(content)
		if err != nil {
			return err
		}
		update.Content = raw
	case "result":
		content := &msg.CustomContent{}
		if err := protocol.Unmarshal(update.Content, content); err != nil {
			return status.Error(codes.InvalidArgument, "invalid typed result")
		}
		if strings.TrimSpace(content.Type) == "" || !json.Valid([]byte(content.Data)) {
			return status.Error(codes.InvalidArgument, "typed result requires a type and valid JSON data")
		}
		raw, err := protocol.Marshal(content)
		if err != nil {
			return err
		}
		update.Content = raw
	default:
		return status.Error(codes.InvalidArgument, "unsupported result type")
	}
	return nil
}

// ExecutorUpdate accepts only authenticated updates to platform-owned work.
func (s *Service) ExecutorUpdate(ctx context.Context, botID string, body []byte) (*model.PublicRun, error) {
	update, err := decodeUpdate(body)
	if err != nil {
		return nil, err
	}
	digest, err := hash(update)
	if err != nil {
		return nil, err
	}
	return s.repo.Report(ctx, update.RunID, "executor:"+update.UpdateKey, digest, func(r *model.Run) error {
		if r.BotID != botID || r.ExecutorType != "third_party" {
			return status.Error(codes.PermissionDenied, "executor does not own this Run")
		}
		if err := s.authorize(ctx, r, false); err != nil {
			return err
		}
		if r.ExecutionGeneration != update.Generation || r.InputVersion != update.InputVersion {
			return status.Error(codes.Aborted, "stale executor generation or input version")
		}
		if update.Type == "run.delivery_ack" && r.ControlOperation != update.Operation {
			return status.Error(codes.Aborted, "delivery acknowledgment belongs to a superseded operation")
		}
		if update.Status == "completed" && update.MsgType == "file" {
			var submitted msg.FileContent
			if err := protocol.Unmarshal(update.Content, &submitted); err != nil {
				return err
			}
			var count int64
			if err := s.svc.DB.WithContext(ctx).Table("file.files").Where("id = ? AND access = 3", submitted.FileId).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return status.Error(codes.PermissionDenied, "file is not publicly shareable")
			}
		}
		return nil
	}, func(tx *gorm.DB, r *model.Run) error {
		switch update.Type {
		case "run.delivery_ack":
			r.DeliveryStatus = "acknowledged"
		case "run.claim":
			if r.ExecutorStatus != "pending" || r.Status == "stopping" {
				return status.Error(codes.FailedPrecondition, "Run already claimed or stopping")
			}
			r.ExecutorStatus = "accepted"
			r.Status = "running"
			r.Stage = "running"
		case "run.update":
			if r.ExecutorStatus != "accepted" && !(r.Status == "stopping" && update.Status == "stopped") {
				return status.Error(codes.FailedPrecondition, "Run must be claimed")
			}
			if update.Sequence != r.ExecutorSequence+1 {
				return status.Error(codes.Aborted, "executor sequence is not continuous")
			}
			if r.Status == "stopping" && update.Status != "stopped" && update.Status != "failed" {
				return status.Error(codes.FailedPrecondition, "stop request must be acknowledged")
			}
			if r.Status == "waiting_input" && update.Status == "running" {
				return status.Error(codes.FailedPrecondition, "waiting Run needs explicit input or resume")
			}
			if r.Status == "waiting_input" && update.Status == "completed" {
				return status.Error(codes.FailedPrecondition, "waiting Run needs explicit input or resume")
			}
			if update.Status == "waiting_input" && !r.ExecutorCapabilities.SupportsInput {
				return status.Error(codes.FailedPrecondition, "executor does not support input")
			}
			if update.Status == "stopped" && r.Status != "stopping" {
				return status.Error(codes.FailedPrecondition, "stop was not requested")
			}
			r.ExecutorSequence = update.Sequence
			r.Stage = update.Status
			if update.Stage != "" {
				r.Stage = update.Stage
			}
			if update.Status == "completed" {
				if update.MsgType == "file" {
					submitted := new(msg.FileContent)
					if err := protocol.Unmarshal(update.Content, submitted); err != nil {
						return err
					}
					var file struct {
						ID       string
						Name     string
						Size     int64
						Ext      string
						MimeType string
					}
					if err := tx.Table("file.files").Select("id, name, size, ext, mime_type").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND access = 3", submitted.FileId).Take(&file).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return status.Error(codes.PermissionDenied, "file is not publicly shareable")
						}
						return err
					}
					canonical, err := protocol.Marshal(&msg.FileContent{FileId: file.ID, Name: file.Name, Size: file.Size, Ext: file.Ext, MimeType: file.MimeType})
					if err != nil {
						return err
					}
					update.Content = canonical
					update.ResultText = file.Name
				}
				r.ResultText = update.ResultText
				r.ResultType = update.MsgType
				r.ResultContent = update.Content
				r.ExecutorStatus = "completed"
				r.OutputStatus = "pending"
				r.Stage = "pending"
				next := *r
				next.Version++
				return repo.QueueRunOutput(tx, &next)
			}
			r.Status = update.Status
			r.WaitingReason = update.WaitingReason
		}
		return nil
	})
}

type ControlInput struct {
	Operation       string
	Message         string
	SubmissionKey   string
	SourceMessageID *string
	ReplyToMsgID    *string
	MessageIDs      []string
	FileIDs         []string
	HistoryGrantID  *string
}

// Control accepts explicit input independently of controller rights. Receipts
// replay only after current membership, source and binding authorization.
func (s *Service) Control(ctx context.Context, id, user string, command ControlInput) (*model.PublicRun, error) {
	if identity.Validate(id) != nil || command.SubmissionKey == "" || len(command.SubmissionKey) > 256 || len(command.Message) > 65536 {
		return nil, status.Error(codes.InvalidArgument, "invalid Run command")
	}
	for _, source := range []*string{command.SourceMessageID, command.ReplyToMsgID, command.HistoryGrantID} {
		if source != nil && identity.Validate(*source) != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid input source identity")
		}
	}
	if err := validateMaterialIDs(command.MessageIDs, command.FileIDs); err != nil {
		return nil, err
	}
	if command.MessageIDs == nil {
		command.MessageIDs = []string{}
	}
	if command.FileIDs == nil {
		command.FileIDs = []string{}
	}
	operation := command.Operation
	if !slices.Contains([]string{"input", "stop", "resume", "close_interaction", "clear_context"}, operation) {
		return nil, status.Error(codes.InvalidArgument, "unknown Run operation")
	}
	if operation == "input" && strings.TrimSpace(command.Message) == "" {
		return nil, status.Error(codes.InvalidArgument, "input message required")
	}
	if operation != "input" && (command.Message != "" || command.SourceMessageID != nil || command.ReplyToMsgID != nil || command.HistoryGrantID != nil || len(command.MessageIDs) != 0 || len(command.FileIDs) != 0) {
		return nil, status.Error(codes.InvalidArgument, "control operation does not accept input materials")
	}
	digest, err := hash(command)
	if err != nil {
		return nil, err
	}
	key := "control:" + user + ":" + command.SubmissionKey
	var materials []Material
	var sources []map[string]any
	var sourceMismatch bool
	authorize := func(r *model.Run) error {
		if err := s.visible(ctx, user, r); err != nil {
			return err
		}
		if err := s.authorize(ctx, r, false); err != nil {
			return err
		}
		if operation != "input" && user != r.RequesterID {
			if err := s.requireAdministrator(ctx, r.ConvID, user); err != nil {
				return err
			}
		}
		if operation == "input" {
			input := Input{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: user, Message: command.Message, SourceMessageID: command.SourceMessageID, MessageIDs: command.MessageIDs, FileIDs: command.FileIDs, HistoryGrantID: command.HistoryGrantID, ReplyInput: true}
			if command.SourceMessageID != nil {
				m, err := s.source(ctx, input, *command.SourceMessageID, true)
				if err != nil {
					return err
				}
				_, binding, err := s.svc.BotRepo.FindByIDWithConvBot(ctx, r.BotID, r.ConvID)
				if err != nil {
					return err
				}
				if m.Seq <= binding.StartSeq || m.SenderType != "user" || m.GetSenderId() != user || m.GetText() == nil {
					return status.Error(codes.PermissionDenied, "input source is not a current contributor message")
				}
				if command.ReplyToMsgID != nil && (r.MessageID == nil || *r.MessageID != *command.ReplyToMsgID || m.GetReplyToId() != *command.ReplyToMsgID) {
					return status.Error(codes.PermissionDenied, "source does not reply to this Run card")
				}
				sourceMismatch = m.GetText().Text != command.Message
				// Receipt replay checks visibility, not a later edit's replacement
				// body. Only a new accepted input must match its current source.
				input.Message = m.GetText().Text
			}
			var err error
			materials, sources, err = s.collectMaterials(ctx, input, 100)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if operation == "close_interaction" || operation == "clear_context" {
		return s.repo.CloseInteraction(ctx, id, key, digest, authorize)
	}
	return s.repo.Report(ctx, id, key, digest, authorize, func(tx *gorm.DB, r *model.Run) error {
		caps := r.ExecutorCapabilities
		if (operation == "input" && !caps.SupportsInput) || (operation == "stop" && !caps.SupportsStop) || (operation == "resume" && !caps.SupportsResume) {
			return status.Error(codes.FailedPrecondition, "executor does not support this operation")
		}
		if r.Status == "stopping" {
			return status.Error(codes.FailedPrecondition, "Run is stopping")
		}
		wasWaiting := r.Status == "waiting_input"
		switch operation {
		case "input":
			if sourceMismatch {
				return status.Error(codes.InvalidArgument, "input differs from its source message")
			}
			if (r.Status != "accepted" && r.Status != "running" && !wasWaiting) || r.OutputStatus != "none" {
				return status.Error(codes.FailedPrecondition, "Run no longer accepts work input")
			}
			r.InputVersion++
			r.InputSources = append(r.InputSources, model.InputSource{Version: r.InputVersion, RequesterID: user, SourceMessageID: command.SourceMessageID, SenderType: "user", MessageIDs: command.MessageIDs, FileIDs: command.FileIDs, HistoryGrantID: command.HistoryGrantID})
			inputID, err := identity.New()
			if err != nil {
				return err
			}
			materialRaw, err := json.Marshal(materials)
			if err != nil {
				return err
			}
			sourcesRaw, err := json.Marshal(sources)
			if err != nil {
				return err
			}
			if err := tx.Create(&model.RunInput{ID: inputID, RunID: r.ID, Version: r.InputVersion, RequesterID: user, SourceMessageID: command.SourceMessageID, InputKey: command.SubmissionKey, Content: command.Message, ContentHash: digest, Materials: materialRaw, Sources: sourcesRaw, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
			var cumulative []Material
			if err := json.Unmarshal(r.Materials, &cumulative); err != nil {
				return err
			}
			cumulative = append(cumulative, materials...)
			r.Materials, err = json.Marshal(cumulative)
			if err != nil {
				return err
			}
			var cumulativeSources []map[string]any
			if err := json.Unmarshal(r.Sources, &cumulativeSources); err != nil {
				return err
			}
			cumulativeSources = append(cumulativeSources, sources...)
			r.Sources, err = json.Marshal(cumulativeSources)
			if err != nil {
				return err
			}
			if wasWaiting {
				r.Status, r.Stage, r.WaitingReason = "running", "input_received", ""
			}
		case "stop":
			r.Status, r.Stage, r.WaitingReason = "stopping", "stop_requested", ""
		case "resume":
			if !wasWaiting {
				return status.Error(codes.FailedPrecondition, "only unfinished waiting work may resume")
			}
			r.ExecutionGeneration++
			r.ExecutorSequence, r.ExecutorStatus, r.Status, r.Stage, r.WaitingReason = 0, "pending", "accepted", "resuming", ""
		}
		r.ControlOperation = operation
		next := *r
		next.Version++
		if r.ExecutorType == "builtin" {
			// Accepted/running work already has one live worker. Only wake a parked
			// waiting Run; an additional queue item would supersede its current lease.
			if wasWaiting {
				return repo.QueueRunExecution(tx, &next)
			}
			return nil
		}
		r.DeliveryStatus = "pending"
		next.DeliveryStatus = "pending"
		return repo.QueueRunDelivery(tx, &next)
	})
}

func (s *Service) requireAdministrator(ctx context.Context, conv, user string) error {
	for page := int32(1); ; page++ {
		result, err := s.messages.GetMembers(userContext(ctx, user), &msg.GetMembersReq{ConversationId: conv, UserId: &user, Pagination: &common.Pagination{Page: page, PageSize: 100}})
		if err != nil {
			return err
		}
		for _, member := range result.Members {
			if member.GetUserId() == user && (member.Role == msg.MemberRole_MEMBER_ROLE_OWNER || member.Role == msg.MemberRole_MEMBER_ROLE_ADMIN) {
				return nil
			}
		}
		if len(result.Members) < 100 {
			break
		}
	}
	return status.Error(codes.PermissionDenied, "Run control requires requester or conversation administrator")
}
