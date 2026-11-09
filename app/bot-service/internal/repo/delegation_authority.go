package repo

import (
	"errors"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func CheckDelegationGrants(tx *gorm.DB, parent, child *model.Run) error {
	for _, id := range child.ResourceGrantIDs {
		var grant resourcegrant.Grant
		found := false
		for _, kind := range []string{"file", "knowledge"} {
			err := tx.Table(resourcegrant.Table(kind)).Where("id = ?", id).Take(&grant).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := resourcegrant.Check(tx, resourcegrant.Access{BotID: parent.BotID, ConvID: parent.ConvID, BindingID: parent.BindingID, RequesterID: parent.RequesterID, Visibility: parent.Visibility, GrantIDs: parent.ResourceGrantIDs}, kind, grant.ResourceID, true, true); err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return status.Error(codes.PermissionDenied, "target resource grant unavailable")
		}
	}
	return nil
}

// Acceptance reserves the first child call. Every later provider call reserves
// independently under the same root barrier; restarts never reset consumption.
func ReserveDelegationOutput(tx *gorm.DB, r *model.Run, tokens int64, first bool) error {
	if r.DelegationRootRunID == nil {
		return nil
	}
	if first {
		var calls int64
		if err := tx.Model(&model.RunModelCall{}).Where("run_id = ?", r.ID).Count(&calls).Error; err != nil {
			return err
		}
		if calls == 0 {
			return nil
		}
	}
	var reserved int64
	if err := tx.Model(&model.Run{}).Select("COALESCE(SUM(delegation_output_tokens),0)").Where("delegation_root_run_id = ?", *r.DelegationRootRunID).Scan(&reserved).Error; err != nil {
		return err
	}
	if tokens <= 0 || reserved+tokens > 16384 {
		return status.Error(codes.ResourceExhausted, "collaboration output reservation exhausted")
	}
	r.DelegationOutputTokens += tokens
	return nil
}

func ReserveDelegationInput(tx *gorm.DB, r *model.Run, bytes int64) error {
	if r.DelegationRootRunID == nil {
		return nil
	}
	var used int64
	if err := tx.Model(&model.Run{}).Select("COALESCE(SUM(delegation_input_bytes),0)").Where("delegation_root_run_id = ?", *r.DelegationRootRunID).Scan(&used).Error; err != nil {
		return err
	}
	if bytes < 0 || used+bytes > 262144 {
		return status.Error(codes.ResourceExhausted, "collaboration input budget exhausted")
	}
	r.DelegationInputBytes += bytes
	return nil
}
