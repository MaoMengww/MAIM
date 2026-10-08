package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
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
	if r.ExecutionGeneration != payload.Generation || r.InputVersion != payload.InputVersion || r.Status == "succeeded" || r.Status == "failed" || r.Status == "stopped" || r.OutputStatus == "pending" {
		return nil
	}
	if r.ControlOperation != payload.Operation {
		return nil
	}
	if (payload.Operation == "" || payload.Operation == "resume") && r.ExecutorStatus != "pending" {
		return nil
	}
	if payload.Operation == "input" && r.DeliveryStatus == "acknowledged" {
		return nil
	}
	if err := s.authorize(ctx, r, false); err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition:
			return s.fail(ctx, r, r.ExecutionGeneration, "executor delivery authorization revoked", false)
		}
		return err
	}
	bot, err := s.svc.BotRepo.GetBot(ctx, r.BotID)
	if err != nil {
		return err
	}
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return err
	}
	// A route change between authorize and this fresh lookup must not send
	// captured Run materials to the new endpoint or use its replacement secret.
	if bot.Status != "active" || bot.Type != "third_party" || bot.ExecutorRevision != config.ExecutorRevision || bot.ConnMode != config.ConnMode || bot.CallbackURL != config.CallbackURL {
		return s.fail(ctx, r, r.ExecutionGeneration, "executor authorization or routing changed", false)
	}
	err = s.deliverPayload(ctx, bot, r.ConvID, item.Payload)
	if err != nil {
		return err
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
	// A fast executor can finish while the transport acknowledgement is in flight.
	if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Aborted {
		return nil
	}
	if err == nil {
		return status.Error(codes.Unavailable, "awaiting executor acceptance")
	}
	return err
}
