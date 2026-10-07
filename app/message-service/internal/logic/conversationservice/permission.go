package conversationservice

import (
	"context"
	stderrors "errors"
	"github.com/maomeng/aim/app/message-service/internal/model"

	"github.com/maomeng/aim/app/message-service/internal/repo"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

// requireRole verifies the operator is a member with at least minRole privilege.
func requireRole(ctx context.Context, r *repo.ConversationRepo, convID, operatorID string, minRole int32) error {
	if minRole <= adminRole {
		conv, err := r.GetConversation(ctx, convID)
		if err != nil {
			return err
		}
		if conv.Type == model.ConvTypeSystem {
			return errors.ErrForbidden
		}
	}
	member, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New(errors.CodeForbidden, "not a member of the conversation")
		}
		return errors.ErrInternal
	}
	if member.MemberType != model.MemberTypeUser || member.UserID == nil || member.Role > minRole {
		return errors.New(errors.CodeForbidden, "insufficient permissions")
	}
	return nil
}

// verifyTargetNotHigher ensures the target does not have equal or higher role than the operator.
func verifyTargetNotHigher(ctx context.Context, r *repo.ConversationRepo, convID, targetID, operatorID string) error {
	target, err := r.GetMember(ctx, convID, targetID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New(errors.CodeForbidden, "target is not a member of the conversation")
		}
		return errors.ErrInternal
	}
	operator, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New(errors.CodeForbidden, "operator is not a member of the conversation")
		}
		return errors.ErrInternal
	}
	if target.Role <= operator.Role {
		return errors.New(errors.CodeForbidden, "cannot operate on member with equal or higher role")
	}
	return nil
}

// ownerRole and adminRole are aliases for readability.
var (
	ownerRole = int32(conversation.MemberRole_MEMBER_ROLE_OWNER)
	adminRole = int32(conversation.MemberRole_MEMBER_ROLE_ADMIN)
)
