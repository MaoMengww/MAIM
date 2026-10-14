package messageservicelogic

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SendBotReplyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBotReplyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBotReplyLogic {
	return &SendBotReplyLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

// The publication receipt is immutable even if the message is later edited or
// hidden. Its content uses the same semantic JSON fingerprint as user sends.
type botOutputSubmission struct {
	BotID              string            `gorm:"primaryKey;type:uuid"`
	BindingID          string            `gorm:"primaryKey;type:uuid"`
	OutputKey          string            `gorm:"primaryKey"`
	ConvID             string            `gorm:"type:uuid"`
	ContentFingerprint model.JSONContent `gorm:"type:jsonb"`
	MessageID          string            `gorm:"type:uuid"`
	Seq                int64
	CreatedAt          time.Time
}

func (botOutputSubmission) TableName() string { return "bot_output_submissions" }

func (l *SendBotReplyLogic) SendBotReply(in *message.SendBotReplyReq) (*message.SendBotReplyResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.BotId, in.BindingId) != nil || (in.ReplyToId != nil && identity.Validate(*in.ReplyToId) != nil) || (in.RequesterId != nil && identity.Validate(*in.RequesterId) != nil) || (in.ControllerId != nil && identity.Validate(*in.ControllerId) != nil) || (in.RunId != nil && identity.Validate(*in.RunId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot sending identity")
	}
	if in.RequesterId == nil && in.RunId == nil {
		return nil, errors.New(errors.CodeForbidden, "requester-free output requires an originating platform Run")
	}
	// Bot credentials and domain calls never borrow a user's access identity.
	if callerUserID(l.ctx) != "" {
		return nil, errors.New(errors.CodeForbidden, "bot output cannot use a user identity")
	}
	if in.OutputKey != nil && (*in.OutputKey == "" || len(*in.OutputKey) > 512 || !utf8.ValidString(*in.OutputKey) || strings.ContainsRune(*in.OutputKey, 0)) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot output key")
	}
	if err := validateBotMetadata(in.RawPayload); err != nil {
		return nil, errors.New(errors.CodeInvalidParam, err.Error())
	}
	kind := in.Type
	if kind == message.MessageType_MESSAGE_TYPE_UNSPECIFIED {
		kind = message.MessageType_MESSAGE_TYPE_TEXT
	}
	var content model.JSONContent
	switch kind {
	case message.MessageType_MESSAGE_TYPE_TEXT:
		if in.File != nil || in.Result != nil || in.Text == "" {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot text")
		}
		content = model.TextContent{Text: in.Text}.ToJSONContent()
	case message.MessageType_MESSAGE_TYPE_FILE:
		if in.File == nil || identity.Validate(in.File.FileId) != nil || in.Text != "" || in.Result != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot file")
		}
	case message.MessageType_MESSAGE_TYPE_RESULT:
		if in.Result == nil || in.Result.Type == "" || !json.Valid([]byte(in.Result.Data)) || in.File != nil || in.Text != "" {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot result")
		}
		content = model.CustomContent{Type: in.Result.Type, Data: in.Result.Data}.ToJSONContent()
	default:
		return nil, errors.New(errors.CodeInvalidParam, "unsupported bot content type")
	}
	msg := &model.Message{ConvID: in.ConversationId, SenderID: &in.BotId, SenderType: "bot", BindingID: &in.BindingId, RequesterID: in.RequesterId, RawPayload: in.RawPayload, MsgType: int32(kind), Content: content, ReplyToMsgID: in.ReplyToId, Status: model.MessageStatusNormal, EditHistory: model.JSONArray{}}
	var receipt *botOutputSubmission
	created := false
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockBotBinding(l.ctx, tx, in.ConversationId, in.BotId, in.BindingId); err != nil {
			return err
		}
		// Resolve display metadata locally; the send hot path never calls Bot control.
		var bot struct {
			Name   string
			Avatar string
			Status string
		}
		if err := tx.Table("bot.bots").Select("name, avatar, status").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", in.BotId).Take(&bot).Error; err != nil || bot.Status != "active" {
			return errors.New(errors.CodeForbidden, "bot is unavailable")
		}
		msg.SenderName, msg.SenderAvatar = bot.Name, bot.Avatar
		controller := in.GetControllerId()
		if controller == "" {
			controller = in.GetRequesterId()
		}
		var runAccess *resourcegrant.Access
		if in.RunId != nil {
			// Runtime source RPCs finish before taking the conversation barrier.
			var run struct {
				ControllerID                string
				ControllerMemberID          *string
				RequesterID                 string
				Visibility                  string
				ResourceGrantIDs            []string `gorm:"serializer:json"`
				Status                      string
				AccessRevoked               bool
				BotAuthorizationVersion     int64
				BindingAuthorizationVersion int64
				Provenance                  json.RawMessage
				Materials                   json.RawMessage
				ResultInvalidated           bool
				SourceMessageID             *string
				ReplyToMsgID                *string
				Origin                      json.RawMessage
			}
			if err := tx.Table("bot.runs").Select("controller_id, controller_member_id, requester_id, visibility, resource_grant_ids, status, access_revoked, bot_authorization_version, binding_authorization_version, provenance, materials, result_invalidated, source_message_id, reply_to_msg_id, origin").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ?", *in.RunId, in.BotId, in.ConversationId, in.BindingId).Take(&run).Error; err != nil {
				return errors.New(errors.CodeForbidden, "Run output authorization unavailable")
			}
			if in.GetRequesterId() != run.RequesterID {
				return errors.New(errors.CodeForbidden, "requester does not match the accepted Run")
			}
			if run.RequesterID == "" {
				var origin struct {
					Kind                  string `json:"kind"`
					AuthorizationUserID   string `json:"authorization_user_id"`
					AuthorizationMemberID string `json:"authorization_member_id"`
					SourceRunID           string `json:"source_run_id"`
					DelegatingBotID       string `json:"delegating_bot_id"`
				}
				if json.Unmarshal(run.Origin, &origin) != nil || (origin.Kind != "rule" && origin.Kind != "delegation") || identity.Validate(origin.AuthorizationUserID) != nil || identity.Validate(origin.AuthorizationMemberID) != nil {
					return errors.New(errors.CodeForbidden, "nonhuman Run authorization unavailable")
				}
				var authorizer struct {
					ID   string
					Role int32
				}
				if err := tx.Table("conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", in.ConversationId, origin.AuthorizationUserID).Take(&authorizer).Error; err != nil || authorizer.ID != origin.AuthorizationMemberID || (origin.Kind == "rule" && authorizer.Role > 2) {
					return errors.New(errors.CodeForbidden, "nonhuman Run authorizer lifecycle revoked")
				}
				if origin.Kind == "delegation" {
					if err := checkDelegationOutputOrigin(tx, in.ConversationId, origin.SourceRunID, origin.DelegatingBotID, 0); err != nil {
						return err
					}
				}
			}
			if run.AccessRevoked || run.ResultInvalidated || run.Status == "stopping" || run.Status == "waiting_input" || run.Status == "waiting_authorization" || run.Status == "stopped" || run.ControllerID != controller {
				return errors.New(errors.CodeForbidden, "Run output permission revoked")
			}
			if err := checkOutputSources(tx, in.ConversationId, in.BindingId, *in.RunId, run.Provenance, run.Materials); err != nil {
				return err
			}
			// Reply identity comes from the accepted Run, not the original message's
			// continued existence. Hydration still applies ordinary IM visibility.
			reply := run.SourceMessageID
			if reply == nil {
				reply = run.ReplyToMsgID
			}
			if in.ReplyToId != nil && (reply == nil || *in.ReplyToId != *reply) {
				return errors.New(errors.CodeForbidden, "reply is outside the accepted Run")
			}
			if run.Visibility != "conversation" {
				return errors.New(errors.CodeForbidden, "personal work cannot be published to a conversation")
			}
			access := resourcegrant.Access{BotID: in.BotId, ConvID: in.ConversationId, BindingID: in.BindingId, RequesterID: run.RequesterID, Visibility: run.Visibility, GrantIDs: run.ResourceGrantIDs}
			runAccess = &access
			if err := resourcegrant.CheckIDs(tx, access, true, true); err != nil {
				return errors.New(errors.CodeForbidden, "Run sharing authorization revoked")
			}
			if err := resourcegrant.CheckRunToolPermissions(tx, access, *in.RunId, true); err != nil {
				return errors.New(errors.CodeForbidden, "Run tool sharing authorization revoked")
			}
			var member struct{ ID string }
			if err := tx.Table("conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", in.ConversationId, controller).Take(&member).Error; err != nil || run.ControllerMemberID == nil || *run.ControllerMemberID != member.ID {
				return ErrNotMember
			}
			var epoch struct {
				BotVersion     int64
				BindingVersion int64
			}
			if err := tx.Raw("SELECT b.authorization_version AS bot_version,c.authorization_version AS binding_version FROM bot.bots b JOIN messaging.conv_bots c ON c.bot_id=b.id WHERE b.id=? AND c.id=?", in.BotId, in.BindingId).Scan(&epoch).Error; err != nil {
				return err
			}
			if epoch.BotVersion != run.BotAuthorizationVersion || epoch.BindingVersion != run.BindingAuthorizationVersion {
				return errors.New(errors.CodeForbidden, "Run authorization epoch revoked")
			}
		} else if controller != "" {
			var count int64
			if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ? AND user_id = ? AND member_type = 'user'", in.ConversationId, controller).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrNotMember
			}
		}
		if kind == message.MessageType_MESSAGE_TYPE_FILE {
			// Public objects or current owner-authorized conversation disclosure only.
			var file struct {
				ID       string
				Name     string
				Size     int64
				Ext      string
				MimeType string
				Access   int32
			}
			if err := tx.Table("file.files").Select("id, name, size, ext, mime_type, access").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", in.File.FileId).Take(&file).Error; err != nil {
				return errors.New(errors.CodeForbidden, "file output unavailable")
			}
			if file.Access != 3 {
				if runAccess == nil {
					return errors.New(errors.CodeForbidden, "file sharing requires resource authorization")
				}
				if _, err := resourcegrant.Check(tx, *runAccess, "file", file.ID, true, true); err != nil {
					return errors.New(errors.CodeForbidden, "file sharing authorization revoked")
				}
			}
			msg.Content = model.FileContent{FileID: file.ID, Name: file.Name, Size: file.Size, Ext: file.Ext, MimeType: file.MimeType}.ToJSONContent()
		}
		if in.RunId == nil {
			if err := validateReplyTarget(l.ctx, l.svcCtx, tx, in.ConversationId, controller, in.ReplyToId); err != nil {
				return err
			}
		}
		// Every retry reaches current identity, resource and metadata checks above
		// before observing a receipt. The conversation lock serializes keys.
		if in.OutputKey != nil {
			fingerprint, err := botOutputFingerprint(msg)
			if err != nil {
				return err
			}
			var existing botOutputSubmission
			err = tx.Where("bot_id = ? AND binding_id = ? AND output_key = ?", in.BotId, in.BindingId, *in.OutputKey).Take(&existing).Error
			if err == nil {
				same, err := sameSubmission(existing.ContentFingerprint, fingerprint)
				if err != nil {
					return err
				}
				if !same {
					return errors.New(errors.CodeConflict, "bot output key was already used with different content")
				}
				receipt = &existing
				return nil
			}
			if !stderrors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			receipt = &botOutputSubmission{BotID: in.BotId, BindingID: in.BindingId, OutputKey: *in.OutputKey, ConvID: in.ConversationId, ContentFingerprint: fingerprint}
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		msg.ID = id
		now := time.Now()
		msg.CreatedAt, msg.UpdatedAt = now, now
		if err := persistMessage(l.ctx, l.svcCtx, tx, msg, msg.SenderName); err != nil {
			return err
		}
		if receipt != nil {
			receipt.MessageID, receipt.Seq, receipt.CreatedAt = msg.ID, msg.Seq, msg.CreatedAt
			if err := tx.Create(receipt).Error; err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		l.Infof("bot message committed: message_id=%s bot_id=%s binding_id=%s", msg.ID, in.BotId, in.BindingId)
	}
	if receipt != nil {
		return &message.SendBotReplyResp{MessageId: receipt.MessageID, Seq: receipt.Seq, CreatedAt: receipt.CreatedAt.Unix()}, nil
	}
	return &message.SendBotReplyResp{MessageId: msg.ID, Seq: msg.Seq, CreatedAt: msg.CreatedAt.Unix()}, nil
}

func botOutputFingerprint(msg *model.Message) (model.JSONContent, error) {
	// Server-resolved display snapshots are not caller content: renaming a Bot
	// must not invalidate a retry of an already committed publication.
	fingerprint, err := originalSubmission(message.MessageType(msg.MsgType), msg.Content, msg.ReplyToMsgID)
	if err != nil {
		return nil, err
	}
	var metadata any
	if msg.RawPayload != "" {
		decoder := json.NewDecoder(bytes.NewBufferString(msg.RawPayload))
		decoder.UseNumber()
		if err := decoder.Decode(&metadata); err != nil {
			return nil, err
		}
	}
	fingerprint["conv_id"] = msg.ConvID
	fingerprint["bot_id"] = msg.SenderID
	fingerprint["binding_id"] = msg.BindingID
	fingerprint["requester_id"] = msg.RequesterID
	fingerprint["metadata"] = metadata
	return fingerprint, nil
}

type outputSourceRun struct {
	ID, BotID, ConvID, BindingID, InteractionID                         string
	RequesterID, Visibility, Status, ResultType                         string
	ParentRunID, DelegationParentRunID                                  *string
	ResourceGrantIDs                                                    []string `gorm:"serializer:json"`
	BranchVersion, BotAuthorizationVersion, BindingAuthorizationVersion int64
	AccessRevoked, ResultInvalidated                                    bool
	Origin, Provenance, Materials, ResultContent                        json.RawMessage
}

func (r *outputSourceRun) access() resourcegrant.Access {
	return resourcegrant.Access{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID, Visibility: r.Visibility, GrantIDs: r.ResourceGrantIDs}
}

type outputSourceMaterial struct {
	Kind           string `json:"kind"`
	ParentRunID    string `json:"parent_run_id"`
	FileID         string `json:"file_id"`
	KnowledgeID    string `json:"knowledge_id"`
	Seq            int64  `json:"seq"`
	HistoryGrantID string `json:"history_grant_id"`
}

func loadOutputSource(tx *gorm.DB, convID, runID string) (*outputSourceRun, error) {
	var run outputSourceRun
	if identity.Validate(runID) != nil || tx.Table("bot.runs").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND conv_id = ?", runID, convID).Take(&run).Error != nil {
		return nil, errors.New(errors.CodeForbidden, "related Run source unavailable")
	}
	return &run, nil
}

func checkOutputSourceParticipation(tx *gorm.DB, run *outputSourceRun) error {
	var binding struct{ ID string }
	if err := tx.Table("messaging.conv_bots c").Select("c.id").Joins("JOIN bot.bots b ON b.id = c.bot_id").Clauses(clause.Locking{Strength: "SHARE"}).Where("c.id = ? AND c.conv_id = ? AND c.bot_id = ? AND c.revoked_at IS NULL AND b.status = 'active' AND c.authorization_version = ? AND b.authorization_version = ?", run.BindingID, run.ConvID, run.BotID, run.BindingAuthorizationVersion, run.BotAuthorizationVersion).Where("COALESCE(c.bot_settings::jsonb->'enabled', 'true'::jsonb) = 'true'::jsonb").Take(&binding).Error; err != nil {
		return errors.New(errors.CodeForbidden, "related Run participation authorization revoked")
	}
	return nil
}

// Only explicit continuation ancestry can inherit a consumed child result.
// ParentRunID is not delegation ancestry; never follow the child's origin here.
func checkOutputDelegationResult(tx *gorm.DB, consumer, child *outputSourceRun) error {
	var origin struct {
		Kind                  string `json:"kind"`
		SourceRunID           string `json:"source_run_id"`
		DelegatingBotID       string `json:"delegating_bot_id"`
		AuthorizationUserID   string `json:"authorization_user_id"`
		AuthorizationMemberID string `json:"authorization_member_id"`
	}
	if child.Status != "succeeded" || child.AccessRevoked || child.ResultInvalidated || child.ConvID != consumer.ConvID || child.Visibility != "conversation" || consumer.Visibility != "conversation" || child.DelegationParentRunID == nil || json.Unmarshal(child.Origin, &origin) != nil || origin.Kind != "delegation" || origin.SourceRunID != *child.DelegationParentRunID {
		return errors.New(errors.CodeForbidden, "delegated result authorization changed")
	}
	var interaction struct{ ID string }
	if err := tx.Table("bot.interactions").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ? AND status = 'active'", consumer.InteractionID, consumer.BotID, consumer.ConvID, consumer.BindingID).Take(&interaction).Error; err != nil {
		return errors.New(errors.CodeForbidden, "delegated result interaction lifecycle revoked")
	}
	current := consumer
	seen := make(map[string]bool)
	for {
		if seen[current.ID] || current.AccessRevoked || current.BotID != consumer.BotID || current.ConvID != consumer.ConvID || current.BindingID != consumer.BindingID || current.InteractionID != consumer.InteractionID || current.Visibility != consumer.Visibility || current.BotAuthorizationVersion != consumer.BotAuthorizationVersion || current.BindingAuthorizationVersion != consumer.BindingAuthorizationVersion {
			return errors.New(errors.CodeForbidden, "delegated result continuation lifecycle revoked")
		}
		seen[current.ID] = true
		if current.ID != consumer.ID && current.Status != "succeeded" && current.Status != "failed" && current.Status != "stopped" {
			return errors.New(errors.CodeForbidden, "delegated result continuation is not terminal")
		}
		if err := checkOutputSourceParticipation(tx, current); err != nil {
			return err
		}
		if current.ID == *child.DelegationParentRunID && current.BotID == origin.DelegatingBotID {
			if current.ID != consumer.ID {
				var inherited []outputSourceMaterial
				if err := json.Unmarshal(current.Materials, &inherited); err != nil {
					return err
				}
				consumed := false
				for _, material := range inherited {
					if material.Kind == "delegation_result" && material.ParentRunID == child.ID {
						consumed = true
						break
					}
				}
				if !consumed {
					return errors.New(errors.CodeForbidden, "continuation has no consumed delegated result")
				}
			}
			break
		}
		if current.ParentRunID == nil {
			return errors.New(errors.CodeForbidden, "delegated result is outside explicit continuation")
		}
		var err error
		current, err = loadOutputSource(tx, consumer.ConvID, *current.ParentRunID)
		if err != nil {
			return err
		}
	}
	var authorizer struct{ ID string }
	if identity.Validate(origin.AuthorizationUserID) != nil || identity.Validate(origin.AuthorizationMemberID) != nil || tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user' AND id = ?", child.ConvID, origin.AuthorizationUserID, origin.AuthorizationMemberID).Take(&authorizer).Error != nil {
		return errors.New(errors.CodeForbidden, "delegated result authorizer lifecycle revoked")
	}
	return checkDelegationOutputOrigin(tx, child.ConvID, origin.SourceRunID, origin.DelegatingBotID, 0)
}

// Consumption grants do not become durable sharing permission. Recheck both
// identities against every material resource, including a returned file result.
func checkOutputDelegationResources(tx *gorm.DB, consumer, child *outputSourceRun) error {
	var materials []outputSourceMaterial
	if err := json.Unmarshal(child.Materials, &materials); err != nil {
		return err
	}
	if child.ResultType == "file" {
		var file message.FileContent
		if err := protocol.Unmarshal(child.ResultContent, &file); err != nil {
			return err
		}
		materials = append(materials, outputSourceMaterial{FileID: file.FileId})
	}
	for _, material := range materials {
		for _, resource := range []struct{ kind, id string }{{"file", material.FileID}, {"knowledge", material.KnowledgeID}} {
			if resource.id == "" {
				continue
			}
			for _, access := range []resourcegrant.Access{child.access(), consumer.access()} {
				if _, err := resourcegrant.Check(tx, access, resource.kind, resource.id, true, true); err != nil {
					return errors.New(errors.CodeForbidden, "delegated material sharing authorization revoked")
				}
			}
		}
	}
	return nil
}

// Publication rechecks real authorization inside the conversation barrier.
// Accepted message identities and versions are provenance, not ongoing grants.
func checkOutputSources(tx *gorm.DB, convID, bindingID, runID string, provenance, materials json.RawMessage) error {
	run, err := loadOutputSource(tx, convID, runID)
	if err != nil {
		return err
	}
	if run.BindingID != bindingID {
		return errors.New(errors.CodeForbidden, "Run output binding changed")
	}
	run.Provenance, run.Materials = provenance, materials
	return checkOutputRunSources(tx, run, make(map[string]bool), make(map[string]bool))
}

func checkOutputRunSources(tx *gorm.DB, run *outputSourceRun, visiting, checked map[string]bool) error {
	if visiting[run.ID] {
		return errors.New(errors.CodeForbidden, "cyclic Run output provenance")
	}
	if checked[run.ID] {
		return nil
	}
	visiting[run.ID] = true
	defer delete(visiting, run.ID)
	var versions []struct {
		resourcegrant.KnowledgeSource
		ParentRunID    string `json:"parent_run_id"`
		SourceRevision int64  `json:"source_revision"`
		FileID         string `json:"file_id"`
	}
	if err := json.Unmarshal(run.Provenance, &versions); err != nil {
		return err
	}
	var sourceMaterials []outputSourceMaterial
	if err := json.Unmarshal(run.Materials, &sourceMaterials); err != nil {
		return err
	}
	delegated := make(map[string]bool)
	referenced := make(map[string]bool)
	for _, material := range sourceMaterials {
		if material.Kind == "delegation_result" {
			if identity.Validate(material.ParentRunID) != nil {
				return errors.New(errors.CodeForbidden, "invalid delegated result source")
			}
			delegated[material.ParentRunID] = true
		}
	}
	for _, version := range versions {
		for _, resource := range []struct{ kind, id string }{{"file", version.FileID}, {"knowledge", version.KnowledgeID}} {
			if resource.id != "" {
				if _, err := resourcegrant.Check(tx, run.access(), resource.kind, resource.id, true, true); err != nil {
					return errors.New(errors.CodeForbidden, "Run material sharing authorization revoked")
				}
			}
		}
		if err := resourcegrant.CheckKnowledgeSource(tx, version.KnowledgeSource, true); err != nil {
			return errors.New(errors.CodeForbidden, "Run knowledge evidence is no longer current")
		}
		if version.ParentRunID == "" {
			continue
		}
		parent, err := loadOutputSource(tx, run.ConvID, version.ParentRunID)
		if err != nil {
			return err
		}
		if parent.AccessRevoked || parent.ResultInvalidated || parent.BranchVersion != version.SourceRevision {
			return errors.New(errors.CodeForbidden, "parent source authorization changed")
		}
		if delegated[parent.ID] {
			if err := checkOutputDelegationResult(tx, run, parent); err != nil {
				return err
			}
			if err := checkOutputDelegationResources(tx, run, parent); err != nil {
				return err
			}
		} else if parent.BotID != run.BotID || parent.BindingID != run.BindingID || parent.InteractionID != run.InteractionID {
			return errors.New(errors.CodeForbidden, "parent source is outside this interaction")
		}
		if err := checkOutputSourceParticipation(tx, parent); err != nil {
			return err
		}
		if err := resourcegrant.CheckIDs(tx, parent.access(), true, true); err != nil {
			return errors.New(errors.CodeForbidden, "related Run sharing authorization revoked")
		}
		if err := resourcegrant.CheckRunToolPermissions(tx, parent.access(), parent.ID, true); err != nil {
			return errors.New(errors.CodeForbidden, "related Run tool sharing authorization revoked")
		}
		if err := checkOutputRunSources(tx, parent, visiting, checked); err != nil {
			return err
		}
		referenced[parent.ID] = true
	}
	for id := range delegated {
		if !referenced[id] {
			return errors.New(errors.CodeForbidden, "delegated result provenance unavailable")
		}
	}
	for _, source := range sourceMaterials {
		if source.HistoryGrantID == "" {
			continue
		}
		var count int64
		if source.Seq > 0 {
			if err := tx.Table("bot_history_grants g").Joins("JOIN conv_bots b ON b.id = g.binding_id").
				Where("g.id = ? AND g.binding_id = ? AND b.conv_id = ? AND ? BETWEEN g.start_seq AND g.end_seq", source.HistoryGrantID, run.BindingID, run.ConvID, source.Seq).
				Where("EXISTS (SELECT 1 FROM conv_members m WHERE m.conv_id = b.conv_id AND m.user_id = g.granted_by AND m.member_type = 'user')").Count(&count).Error; err != nil {
				return err
			}
		}
		if count != 1 {
			return errors.New(errors.CodeForbidden, "history source authorization revoked")
		}
	}
	checked[run.ID] = true
	return nil
}

// All membership changes take the same conversation lock. Checking the exact
// binding ID prevents an old executor from acquiring a replacement lifecycle.
func lockBotBinding(ctx context.Context, tx *gorm.DB, convID, botID, bindingID string) (*model.ConvBot, error) {
	var conv model.Conversation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", convID).Take(&conv).Error; err != nil {
		return nil, err
	}
	var binding model.ConvBot
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conv_id = ? AND bot_id = ? AND revoked_at IS NULL", bindingID, convID, botID).
		Where("EXISTS (SELECT 1 FROM bot.bots b WHERE b.id = conv_bots.bot_id AND b.status = 'active')").Take(&binding).Error; err != nil {
		return nil, ErrBotNotInConversation
	}
	if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
		return nil, ErrBotNotInConversation
	}
	return &binding, nil
}

// Recheck under the conversation barrier: revocation after worker authorization
// cannot publish output. The source Run's terminal state does not hide its work.
func checkDelegationOutputOrigin(tx *gorm.DB, convID, sourceRunID, delegatingBotID string, depth int) error {
	if depth >= 3 || identity.Validate(sourceRunID) != nil || identity.Validate(delegatingBotID) != nil {
		return errors.New(errors.CodeForbidden, "invalid delegation output origin")
	}
	var source struct {
		BotID                       string
		BindingID                   string
		RequesterID                 string
		ControllerID                string
		ControllerMemberID          *string
		Visibility                  string
		ResourceGrantIDs            []string `gorm:"serializer:json"`
		AccessRevoked               bool
		ResultInvalidated           bool
		BotAuthorizationVersion     int64
		BindingAuthorizationVersion int64
		Origin                      json.RawMessage
	}
	if err := tx.Table("bot.runs").Where("id = ? AND conv_id = ? AND bot_id = ?", sourceRunID, convID, delegatingBotID).Take(&source).Error; err != nil || source.AccessRevoked || source.ResultInvalidated || source.Visibility != "conversation" {
		return errors.New(errors.CodeForbidden, "delegation source authorization revoked")
	}
	var binding struct{ ID string }
	if err := tx.Table("messaging.conv_bots c").Select("c.id").Joins("JOIN bot.bots b ON b.id = c.bot_id").Clauses(clause.Locking{Strength: "SHARE"}).Where("c.id = ? AND c.conv_id = ? AND c.bot_id = ? AND c.revoked_at IS NULL AND b.status = 'active' AND c.authorization_version = ? AND b.authorization_version = ?", source.BindingID, convID, source.BotID, source.BindingAuthorizationVersion, source.BotAuthorizationVersion).Where("COALESCE(c.bot_settings::jsonb->'enabled', 'true'::jsonb) = 'true'::jsonb").Take(&binding).Error; err != nil {
		return errors.New(errors.CodeForbidden, "delegation source participation revoked")
	}
	var member struct{ ID string }
	if err := tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", convID, source.ControllerID).Take(&member).Error; err != nil || source.ControllerMemberID == nil || *source.ControllerMemberID != member.ID {
		return ErrNotMember
	}
	access := resourcegrant.Access{BotID: source.BotID, ConvID: convID, BindingID: source.BindingID, RequesterID: source.RequesterID, Visibility: source.Visibility, GrantIDs: source.ResourceGrantIDs}
	if err := resourcegrant.CheckIDs(tx, access, true, true); err != nil {
		return errors.New(errors.CodeForbidden, "delegation source sharing revoked")
	}
	if err := resourcegrant.CheckRunToolPermissions(tx, access, sourceRunID, true); err != nil {
		return errors.New(errors.CodeForbidden, "delegation source tool sharing revoked")
	}
	if len(source.Origin) == 0 || string(source.Origin) == "null" {
		return nil
	}
	var origin struct {
		Kind                  string `json:"kind"`
		SourceRunID           string `json:"source_run_id"`
		DelegatingBotID       string `json:"delegating_bot_id"`
		AuthorizationUserID   string `json:"authorization_user_id"`
		AuthorizationMemberID string `json:"authorization_member_id"`
	}
	if err := json.Unmarshal(source.Origin, &origin); err != nil {
		return err
	}
	var authorizer struct {
		ID   string
		Role int32
	}
	if identity.Validate(origin.AuthorizationUserID) != nil || identity.Validate(origin.AuthorizationMemberID) != nil {
		return errors.New(errors.CodeForbidden, "delegation source authorizer unavailable")
	}
	if err := tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", convID, origin.AuthorizationUserID).Take(&authorizer).Error; err != nil || authorizer.ID != origin.AuthorizationMemberID || (origin.Kind == "rule" && authorizer.Role > 2) {
		return errors.New(errors.CodeForbidden, "delegation source authorizer revoked")
	}
	if origin.Kind == "delegation" {
		return checkDelegationOutputOrigin(tx, convID, origin.SourceRunID, origin.DelegatingBotID, depth+1)
	}
	return nil
}
