package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/repo"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// requireRole verifies the operator is a member with at least minRole privilege.
func requireRole(ctx context.Context, r repo.RepoInterface, convID, operatorID int64, minRole int32) error {
	member, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return status.Error(codes.PermissionDenied, "not a member of the conversation")
		}
		return status.Error(codes.Internal, "internal server error")
	}
	if member.Role > minRole {
		return status.Error(codes.PermissionDenied, "insufficient permissions")
	}
	return nil
}

// verifyTargetNotHigher ensures the target does not have equal or higher role than the operator.
func verifyTargetNotHigher(ctx context.Context, r repo.RepoInterface, convID, targetID, operatorID int64) error {
	target, err := r.GetMember(ctx, convID, targetID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return status.Error(codes.PermissionDenied, "target is not a member of the conversation")
		}
		return status.Error(codes.Internal, "internal server error")
	}
	operator, err := r.GetMember(ctx, convID, operatorID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return status.Error(codes.PermissionDenied, "operator is not a member of the conversation")
		}
		return status.Error(codes.Internal, "internal server error")
	}
	if target.Role <= operator.Role {
		return status.Error(codes.PermissionDenied, "cannot operate on member with equal or higher role")
	}
	return nil
}

// ownerRole and adminRole are aliases for readability.
var (
	ownerRole = int32(conversation.MemberRole_MEMBER_ROLE_OWNER)
	adminRole = int32(conversation.MemberRole_MEMBER_ROLE_ADMIN)
)
