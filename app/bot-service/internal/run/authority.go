package run

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// authorityState is a cheap current-row boundary, not an event-driven cache.
func (s *Service) authorityState(ctx context.Context, r *model.Run) (string, error) {
	if r.AccessRevoked {
		return r.StopReason, nil
	}
	var binding model.ConvBot
	err := s.svc.DB.WithContext(ctx).Where("id = ? AND bot_id = ? AND conv_id = ?", r.BindingID, r.BotID, r.ConvID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "binding_revoked", nil
	}
	if err != nil {
		return "", err
	}
	if binding.RevokedAt != nil {
		return "binding_revoked", nil
	}
	if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
		return "participation_revoked", nil
	}
	if binding.AuthorizationVersion != r.BindingAuthorizationVersion {
		return "participation_revoked", nil
	}
	var bot model.Bot
	if err := s.svc.DB.WithContext(ctx).Where("id = ?", r.BotID).First(&bot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "bot_disabled", nil
		}
		return "", err
	}
	if bot.Status != "active" || bot.AuthorizationVersion != r.BotAuthorizationVersion {
		return "bot_disabled", nil
	}
	if err := resourcegrant.CheckIDs(s.svc.DB.WithContext(ctx), resourceAccess(r), r.Visibility != "personal", false); err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return "resource_authorization_revoked", nil
		}
		return "", err
	}
	if err := s.currentRunTools(ctx, r); err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return "tool_authorization_revoked", nil
		}
		return "", err
	}
	member, _, err := repo.RunMember(s.svc.DB.WithContext(ctx), r.ConvID, r.ControllerID)
	if status.Code(err) == codes.PermissionDenied || (err == nil && (r.ControllerMemberID == nil || *r.ControllerMemberID != member)) {
		return "controller_membership_lost", nil
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

func (s *Service) reconcile(ctx context.Context, r *model.Run) (*model.Run, error) {
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return r, err
	}
	if repo.TerminalRun(r) {
		return r, nil
	}
	if len(r.ControlUserIDs) > 0 {
		var stale []string
		for user, incarnation := range r.ControlMemberships {
			member, _, err := repo.RunMember(s.svc.DB.WithContext(ctx), r.ConvID, user)
			if status.Code(err) == codes.PermissionDenied || (err == nil && member != incarnation) {
				stale = append(stale, user)
			} else if err != nil {
				return r, err
			}
		}
		if len(stale) > 0 {
			current, err := s.repo.ChangeLifecycle(ctx, r.ID, 0, func(tx *gorm.DB, current *model.Run) error {
				for _, user := range stale {
					member, _, err := repo.RunMember(tx, current.ConvID, user)
					if err != nil && status.Code(err) != codes.PermissionDenied {
						return err
					}
					if member != current.ControlMemberships[user] {
						delete(current.ControlMemberships, user)
					}
				}
				controls := make([]string, 0, len(current.ControlUserIDs))
				for _, user := range current.ControlUserIDs {
					if _, ok := current.ControlMemberships[user]; ok {
						controls = append(controls, user)
					}
				}
				current.ControlUserIDs = controls
				return nil
			})
			if err != nil {
				return r, err
			}
			r = current
		}
	}
	reason, err := s.authorityState(ctx, r)
	if err != nil {
		return r, err
	}
	if reason != "" && !(reason == "controller_membership_lost" && (r.Status == "waiting_authorization" || r.Status == "stopping")) && r.StopReason != reason && !(r.AccessRevoked && r.StopReason == reason) {
		r, err = s.repo.ChangeLifecycle(ctx, r.ID, 0, func(tx *gorm.DB, current *model.Run) error {
			if current.Version != r.Version {
				return status.Error(codes.Aborted, "Run changed during lifecycle reconciliation")
			}
			if reason == "controller_membership_lost" {
				if current.Status == "stopping" {
					return nil
				}
				current.Status, current.Stage, current.WaitingReason = "waiting_authorization", "awaiting_takeover", reason
				current.StreamStatus = "closed"
				if current.ExecutorType == "builtin" {
					current.ExecutionGeneration++
					if err := tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested'", current.ID).Updates(map[string]any{"status": "cancelled", "completed_at": time.Now()}).Error; err != nil {
						return err
					}
				}
				if current.ExecutorType == "third_party" {
					current.StopStatus, current.ControlOperation = "requested", "stop"
					now := time.Now()
					current.StopRequestedAt = &now
					current.StopReason = reason
					next := *current
					next.Version++
					return repo.QueueRunDelivery(tx, &next)
				}
				return nil
			}
			current.AccessRevoked = true
			return requestRunStop(tx, current, reason)
		})
		if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Aborted {
			return s.repo.Get(ctx, r.ID)
		}
		if err != nil {
			return r, err
		}
	}
	if (r.StopStatus == "requested") && r.ExecutorType == "third_party" && r.StopRequestedAt != nil && time.Since(*r.StopRequestedAt) >= 5*time.Second {
		r, err = s.repo.ChangeLifecycle(ctx, r.ID, 0, func(_ *gorm.DB, current *model.Run) error {
			if current.StopStatus == "requested" && current.StopRequestedAt != nil && time.Since(*current.StopRequestedAt) >= 5*time.Second {
				current.StopStatus, current.Stage = "unknown", "stop_unconfirmed"
			}
			return nil
		})
	}
	return r, err
}

func requestRunStop(tx *gorm.DB, r *model.Run, reason string) error {
	if r.ExecutorStatus == "completed" {
		r.StreamStatus, r.StopReason = "closed", "completed_before_stop"
		var receipt struct{ MessageID string }
		if err := tx.Table("messaging.bot_output_submissions").Select("message_id").Where("bot_id = ? AND binding_id = ? AND output_key = ?", r.BotID, r.BindingID, r.ID+":final").Take(&receipt).Error; err == nil {
			r.Status, r.Stage, r.OutputStatus, r.MessageID = "succeeded", "committed", "committed", &receipt.MessageID
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if r.AccessRevoked {
			r.Status, r.Stage, r.Error, r.OutputStatus = "failed", "output_revoked", reason, "failed"
			return nil
		}
		return nil
	}
	if r.StopStatus == "requested" || r.StopStatus == "unknown" {
		if reason != "user_requested" {
			r.StopReason, r.Status, r.Stage, r.WaitingReason = reason, "stopping", "stop_requested", ""
		}
		return nil
	}
	if r.ExecutorStatus == "stopped" && r.StopStatus == "confirmed" {
		r.Status, r.Stage = "stopped", "stopped"
		return nil
	}
	r.Status, r.Stage, r.WaitingReason = "stopping", "stop_requested", ""
	r.StopStatus, r.StreamStatus, r.StopReason, r.ControlOperation = "requested", "closed", reason, "stop"
	now := time.Now()
	r.StopRequestedAt = &now
	next := *r
	next.Version++
	if r.ExecutorType == "third_party" {
		r.DeliveryStatus, next.DeliveryStatus = "pending", "pending"
		return repo.QueueRunDelivery(tx, &next)
	}
	return repo.QueueRunExecution(tx, &next)
}

func (s *Service) monitorAuthority(ctx context.Context) {
	for ctx.Err() == nil {
		var runs []model.Run
		if err := s.svc.DB.WithContext(ctx).Where("status NOT IN ('succeeded','failed','stopped')").Find(&runs).Error; err == nil {
			for i := range runs {
				if _, err := s.reconcile(ctx, &runs[i]); err != nil && ctx.Err() == nil {
					s.svc.Logger.Errorf("Run authority reconciliation failed: code=%s", status.Code(err))
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// The guard cancels an in-flight provider/tool RPC. The synchronous boundary
// below still rechecks current authority before every next action.
func (s *Service) guardExecution(parent context.Context, r *model.Run, generation int64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			current, err := s.repo.Get(ctx, r.ID)
			if err != nil {
				cancel()
				return
			}
			current, err = s.reconcile(ctx, current)
			if err != nil || current.ExecutionGeneration != generation || repo.TerminalRun(current) || current.Status == "stopping" || (current.Status == "waiting_authorization" && current.Stage != "pending_action") {
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}

func (s *Service) executionBoundary(ctx context.Context, r *model.Run, generation int64) (*model.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.repo.Get(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	current, err = s.reconcile(ctx, current)
	if err != nil {
		return nil, err
	}
	if current.ExecutionGeneration != generation {
		return nil, status.Error(codes.Aborted, "execution generation superseded")
	}
	if repo.TerminalRun(current) || current.Status == "stopping" || current.Status == "waiting_authorization" {
		return nil, status.Error(codes.FailedPrecondition, "Run execution is suspended")
	}
	if err := s.authorize(ctx, current, false); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) confirmBuiltinStop(ctx context.Context, r *model.Run, generation int64) error {
	_, err := s.repo.ChangeLifecycle(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if current.Status != "stopping" {
			return nil
		}
		current.Status, current.Stage, current.StopStatus, current.StreamStatus, current.ExecutorStatus = "stopped", "stopped", "confirmed", "closed", "stopped"
		if current.OutputStatus == "pending" || current.OutputStatus == "generated" {
			current.OutputStatus = "failed"
		}
		return tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested'", current.ID).Updates(map[string]any{"status": "cancelled", "completed_at": time.Now()}).Error
	})
	if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Aborted {
		return nil
	}
	return err
}

// A stopped executor receives a minimal receipt, not cached materials/results.
func stopReceipt(r *model.PublicRun) *model.PublicRun {
	r.ResultText, r.ResultType, r.ResultContent, r.Error = "", "", nil, ""
	r.WaitingReason = ""
	r.Sources, r.InputSources = []map[string]string{}, []model.InputSource{}
	return r
}

func sharedMaterials(r *model.Run) bool {
	if r.Visibility == "personal" {
		return false
	}
	var materials []Material
	if json.Unmarshal(r.Materials, &materials) != nil {
		return false
	}
	for _, m := range materials {
		if m.HistoryGrantID != "" || (m.MessageID != "" && !m.Shared) {
			return false
		}
	}
	return true
}

func (s *Service) decorateControl(ctx context.Context, public *model.PublicRun, user string) error {
	allowed := false
	r, err := s.repo.Get(ctx, public.ID)
	if err != nil {
		return err
	}
	effective := make([]string, 0, len(r.ControlUserIDs))
	for _, controller := range r.ControlUserIDs {
		member, _, err := repo.RunMember(s.svc.DB.WithContext(ctx), r.ConvID, controller)
		if err != nil && status.Code(err) != codes.PermissionDenied {
			return err
		}
		if err == nil && r.ControlMemberships[controller] == member {
			effective = append(effective, controller)
		}
	}
	public.ControlUserIDs = effective
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return err
	}
	reason, err := s.authorityState(ctx, r)
	if err != nil {
		return err
	}
	if reason == "binding_revoked" || reason == "bot_disabled" || reason == "participation_revoked" {
		public.InteractionStatus, public.StopReason = "closed", reason
	}
	err = s.visible(ctx, user, r)
	if err == nil {
		public.InputSources, err = s.acceptedInputSources(ctx, r, user)
	}
	if err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied, codes.NotFound, codes.Unauthenticated:
			stopReceipt(public)
		default:
			return err
		}
	} else {
		current := r.Public()
		public.Sources, public.ContextNotice = current.Sources, current.ContextNotice
		public.ResultText, public.ResultType, public.ResultContent = current.ResultText, current.ResultType, current.ResultContent
	}
	if err := repo.RunController(s.svc.DB.WithContext(ctx), r, user); err == nil {
		allowed = (reason == "" || reason == "controller_membership_lost") && r.InteractionStatus == "active"
	} else if status.Code(err) != codes.PermissionDenied {
		return err
	}
	public.CanControl = &allowed
	s.decorateActions(ctx, public, user)
	return nil
}

// acceptedInputSources projects the single persisted accepted body, never the
// source message's current text. The caller has already checked visibility.
func (s *Service) acceptedInputSources(ctx context.Context, r *model.Run, user string) ([]model.InputSource, error) {
	inputs, err := s.repo.InputContents(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	contentByVersion := make(map[int64]*string, len(inputs))
	for _, input := range inputs {
		if input.Invalidated {
			input.Content = ""
		}
		contentByVersion[input.Version] = &input.Content
	}
	sources := slices.Clone(r.InputSources)
	for i := range sources {
		sources[i].Content = contentByVersion[sources[i].Version]
	}
	// Membership and resource grants may change while accepted bodies load.
	if err := s.visible(ctx, user, r); err != nil {
		return nil, err
	}
	return sources, nil
}
