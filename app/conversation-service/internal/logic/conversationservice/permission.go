package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/repo"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

// requireRole verifies the operator is a member with at least minRole privilege.
func requireRole(ctx context.Context, r repo.RepoInterface, convID, operatorID int64, minRole int32) error {
	member, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.New(errors.CodeForbidden, "not a member of the conversation")
		}
		return errors.ErrInternal
	}
	if member.Role > minRole {
		return errors.New(errors.CodeForbidden, "insufficient permissions")
	}
	return nil
}

// verifyTargetNotHigher ensures the target does not have equal or higher role than the operator.
func verifyTargetNotHigher(ctx context.Context, r repo.RepoInterface, convID, targetID, operatorID int64) error {
	target, err := r.GetMember(ctx, convID, targetID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.New(errors.CodeForbidden, "target is not a member of the conversation")
		}
		return errors.ErrInternal
	}
	operator, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
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
