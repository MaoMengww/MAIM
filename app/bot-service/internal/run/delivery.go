package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func (s *Service) deliverRun(ctx context.Context, item *model.RunOutbox) error {
	r, err := s.repo.Get(ctx, item.RunID)
	if err != nil {
		return err
	}
	var payload struct {
		Generation   int64  `json:"generation"`
		InputVersion int64  `json:"input_version"`
		Operation    string `json:"operation"`
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return err
	}
	if r.ExecutionGeneration != payload.Generation || r.InputVersion != payload.InputVersion || repo.TerminalRun(r) || r.OutputStatus == "pending" || r.ControlOperation != payload.Operation {
		return nil
	}
	stop := payload.Operation == "stop"
	if stop && (r.StopStatus == "confirmed" || r.StopStatus == "rejected") {
		return nil
	}
	if !stop {
		if (payload.Operation == "" || payload.Operation == "resume") && r.ExecutorStatus != "pending" {
			return nil
		}
		if payload.Operation == "input" && r.DeliveryStatus == "acknowledged" {
			return nil
		}
		current, err := s.reconcile(ctx, r)
		if err != nil {
			return err
		}
		r = current
		if r.Status == "stopping" || r.Status == "waiting_authorization" || r.StreamStatus == "closed" {
			return nil
		}
		if err := s.authorize(ctx, r, false); err != nil {
			switch status.Code(err) {
			case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition:
				_, err = s.repo.ChangeLifecycle(ctx, r.ID, r.ExecutionGeneration, func(tx *gorm.DB, current *model.Run) error {
					return requestRunStop(tx, current, "authorization_revoked")
				})
				return err
			}
			return err
		}
	}
	bot, err := s.svc.BotRepo.GetBot(ctx, r.BotID)
	if err != nil {
		return err
	}
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return err
	}
	// Stop may use the same current route after disabled/revoked participation,
	// but never copy materials or a secret to a replacement endpoint.
	if bot.Type != "third_party" || bot.ConnMode != config.ConnMode || bot.CallbackURL != config.CallbackURL || (!stop && (bot.Status != "active" || bot.ExecutorRevision != config.ExecutorRevision)) {
		if stop {
			return status.Error(codes.Unavailable, "original executor stop route unavailable")
		}
		_, err = s.repo.ChangeLifecycle(ctx, r.ID, r.ExecutionGeneration, func(tx *gorm.DB, current *model.Run) error {
			return requestRunStop(tx, current, "executor_routing_changed")
		})
		return err
	}
	if err := s.deliverPayload(ctx, bot, r.ConvID, item.Payload); err != nil {
		return err
	}
	if stop {
		return status.Error(codes.Unavailable, "awaiting actual executor stop confirmation")
	}
	if bot.ConnMode == "ws" {
		return status.Error(codes.Unavailable, "awaiting executor delivery acknowledgement or claim")
	}
	_, err = s.repo.Change(ctx, r.ID, r.ExecutionGeneration, func(_ *gorm.DB, current *model.Run) error {
		if current.InputVersion != payload.InputVersion {
			return status.Error(codes.Aborted, "input delivery superseded")
		}
		if current.DeliveryStatus == "pending" {
			current.DeliveryStatus = "delivered"
		}
		return nil
	})
	if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Aborted {
		return nil
	}
	if err == nil {
		return status.Error(codes.Unavailable, "awaiting executor acceptance")
	}
	return err
}
