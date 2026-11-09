package resourcegrant

import (
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

// HistorySource records the provenance of an accepted on-demand text snapshot.
// It is not a live authorization dependency.
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
