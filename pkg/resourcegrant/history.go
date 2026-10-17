package resourcegrant

import (
	"encoding/json"
	"errors"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HistoryGrant is messaging's explicit range, never inferred from membership or
// issued by the model. Reading this record does not read any message content.
type HistoryGrant struct {
	ID, BindingID, GrantedBy string
	StartSeq, EndSeq         int64
}

// HistorySource marks an on-demand read, unlike an explicitly accepted input
// snapshot. Keep it in provenance even when its material has been compacted.
type HistorySource struct {
	MessageID         string `json:"message_id,omitempty"`
	AcceptedEditCount int32  `json:"accepted_edit_count"`
	HistoryGrantID    string `json:"history_grant_id,omitempty"`
	HistorySeq        int64  `json:"history_seq,omitzero"`
}

func CheckHistoryGrant(tx *gorm.DB, access Access, grantID string, lock bool) (*HistoryGrant, error) {
	if access.RequesterID != "" && identity.Validate(access.RequesterID) != nil {
		return nil, status.Error(codes.PermissionDenied, "history requester unavailable")
	}
	if identity.Validate(grantID) != nil || identity.Validate(access.BotID) != nil || identity.Validate(access.ConvID) != nil || identity.Validate(access.BindingID) != nil {
		return nil, status.Error(codes.PermissionDenied, "history authorization unavailable")
	}
	query := tx.Table("messaging.bot_history_grants g").Select("g.id,g.binding_id,g.granted_by,g.start_seq,g.end_seq").
		Joins("JOIN messaging.conv_bots b ON b.id = g.binding_id").
		Joins("JOIN messaging.conv_members m ON m.conv_id = b.conv_id AND m.user_id = g.granted_by AND m.member_type = 'user'").
		Joins("JOIN bot.bots bot ON bot.id = b.bot_id").
		Where("g.id = ? AND b.id = ? AND b.conv_id = ? AND b.bot_id = ? AND b.revoked_at IS NULL AND bot.status = 'active'", grantID, access.BindingID, access.ConvID, access.BotID).
		Where("COALESCE(b.bot_settings::jsonb->'enabled', 'true'::jsonb) = 'true'::jsonb")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "g, b, m, bot", Raw: true}})
	}
	var grant HistoryGrant
	if err := query.Take(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.PermissionDenied, "history authorization unavailable")
		}
		return nil, err
	}
	if grant.StartSeq < 1 || grant.EndSeq < grant.StartSeq || sequence.Validate(grant.EndSeq) != nil {
		return nil, status.Error(codes.PermissionDenied, "invalid history authorization range")
	}
	return &grant, nil
}

// CheckHistorySource rechecks the live messaging source, not just its grant.
// Callers publishing or accepting work hold the conversation barrier as well.
// Versions without history fields remain durable accepted-input facts.
func CheckHistorySource(tx *gorm.DB, access Access, source HistorySource, lock bool) error {
	if source.HistoryGrantID == "" && source.HistorySeq == 0 {
		return nil
	}
	return newSourceFence(tx, lock).checkHistory(access, source)
}

func (f *sourceFence) checkHistory(access Access, source HistorySource) error {
	if source.HistoryGrantID == "" && source.HistorySeq == 0 {
		return nil
	}
	if identity.Validate(source.MessageID) != nil || source.HistorySeq < 1 || sequence.Validate(source.HistorySeq) != nil || source.AcceptedEditCount < 0 {
		return status.Error(codes.PermissionDenied, "invalid history source")
	}
	if f.history[source.MessageID] || len(f.history)+len(f.visiting) >= maxSourceAncestry {
		return status.Error(codes.PermissionDenied, "cyclic or excessive history source ancestry")
	}
	f.history[source.MessageID] = true
	defer delete(f.history, source.MessageID)
	grant, err := CheckHistoryGrant(f.tx, access, source.HistoryGrantID, f.lock)
	if err != nil {
		return err
	}
	if source.HistorySeq < grant.StartSeq || source.HistorySeq > grant.EndSeq {
		return status.Error(codes.PermissionDenied, "history source is outside its grant")
	}
	query := f.tx.Table("messaging.messages msg").Select("msg.id,msg.sender_id,msg.binding_id,msg.sender_type,msg.raw_payload").
		Where("msg.id = ? AND msg.conv_id = ? AND msg.seq = ? AND msg.edit_count = ? AND msg.status <> 2", source.MessageID, access.ConvID, source.HistorySeq, source.AcceptedEditCount).
		Where("NOT EXISTS (SELECT 1 FROM messaging.personal_message_deletions d WHERE d.conv_id = msg.conv_id AND d.message_id = msg.id AND (d.user_id = ? OR d.user_id = NULLIF(?, '')::uuid))", grant.GrantedBy, access.RequesterID)
	if f.lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "msg"}})
	}
	var message struct {
		ID, SenderType, RawPayload string
		SenderID, BindingID        *string
	}
	if err := query.Take(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Error(codes.PermissionDenied, "history source is no longer current or visible")
		}
		return err
	}
	if message.SenderType != "bot" || message.RawPayload == "" {
		return nil
	}
	var metadata struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal([]byte(message.RawPayload), &metadata) != nil || identity.Validate(metadata.RunID) != nil {
		return nil // Third-party metadata alone never establishes a platform Run.
	}
	run, err := f.loadRun(access.ConvID, metadata.RunID)
	if err != nil {
		return err
	}
	if message.SenderID == nil || *message.SenderID != run.BotID || message.BindingID == nil || *message.BindingID != run.BindingID || run.ConvID != access.ConvID || run.Visibility != "conversation" || run.AccessRevoked || run.ResultInvalidated {
		return status.Error(codes.PermissionDenied, "history result source authorization revoked")
	}
	return f.checkRun(run)
}
