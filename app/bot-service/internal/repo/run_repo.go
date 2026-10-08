package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RunRepo struct{ DB *database.DB }

func NewRunRepo(db *database.DB) *RunRepo { return &RunRepo{DB: db} }

func (r *RunRepo) Get(ctx context.Context, id string) (*model.Run, error) {
	var run model.Run
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&run).Error
	return &run, err
}
func (r *RunRepo) FindInput(ctx context.Context, botID, bindingID, requesterID, key string) (*model.Run, error) {
	var run model.Run
	err := r.DB.WithContext(ctx).Where("bot_id = ? AND binding_id = ? AND requester_id = ? AND input_key = ?", botID, bindingID, requesterID, key).First(&run).Error
	return &run, err
}
func (r *RunRepo) List(ctx context.Context, convID string, limit int) ([]model.Run, error) {
	runs := make([]model.Run, 0)
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).Order("created_at DESC,id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}
func queueRun(tx *gorm.DB, run *model.Run, kind string) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	raw := json.RawMessage(`{}`)
	if kind == "updated" {
		raw, err = json.Marshal(map[string]any{"type": "bot.run.updated", "conv_id": run.ConvID, "bot_id": run.BotID, "run": run.Public()})
		if err != nil {
			return err
		}
	}
	if kind == "deliver" {
		var inputs []model.RunInput
		if err := tx.Where("run_id = ?", run.ID).Order("version ASC").Find(&inputs).Error; err != nil {
			return err
		}
		var text strings.Builder
		for i, input := range inputs {
			if i > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(input.Content)
		}
		raw, err = json.Marshal(map[string]any{"type": "bot.run.request", "run": run.Public(), "generation": run.ExecutionGeneration, "input_version": run.InputVersion, "input": map[string]any{"text": text.String(), "materials": run.Materials, "sources": run.InputSources}, "operation": run.ControlOperation})
		if err != nil {
			return err
		}
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.RunOutbox{ID: id, RunID: run.ID, Kind: kind, Version: run.Version, Payload: raw, AvailableAt: time.Now()}).Error
}

// Accept serializes retry identity and writes the accepted input, work and all
// publication intents in one Bot-domain transaction.
func (r *RunRepo) Accept(ctx context.Context, run *model.Run, input string) (*model.Run, error) {
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key := run.BotID + ":" + run.BindingID + ":" + run.RequesterID + ":" + run.InputKey
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", key).Error; err != nil {
			return err
		}
		var prior model.Run
		err := tx.Where("bot_id = ? AND binding_id = ? AND requester_id = ? AND input_key = ?", run.BotID, run.BindingID, run.RequesterID, run.InputKey).First(&prior).Error
		if err == nil {
			if prior.InputHash != run.InputHash {
				return status.Error(codes.AlreadyExists, "submission key has different content")
			}
			*run = prior
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Capture the existing lifecycle; rejoining never revives a stale input.
		var binding model.ConvBot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", run.BindingID, run.BotID, run.ConvID).First(&binding).Error; err != nil {
			return err
		}
		var bot model.Bot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", run.BotID).First(&bot).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.Interaction{ID: run.InteractionID, BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID}).Error; err != nil {
			return err
		}
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.RunInput{ID: id, RunID: run.ID, Version: 1, RequesterID: run.RequesterID, SourceMessageID: run.SourceMessageID, InputKey: run.InputKey, Content: input, ContentHash: run.InputHash}).Error; err != nil {
			return err
		}
		kind := "execute"
		if run.ExecutorType == "third_party" {
			kind = "deliver"
		}
		if err := queueRun(tx, run, kind); err != nil {
			return err
		}
		return queueRun(tx, run, "updated")
	})
	return run, err
}

// Change fences every worker mutation with its current execution generation.
func (r *RunRepo) Change(ctx context.Context, id string, generation int64, fn func(*gorm.DB, *model.Run) error) (*model.Run, error) {
	var run model.Run
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; err != nil {
			return err
		}
		if generation > 0 && run.ExecutionGeneration != generation {
			return status.Error(codes.Aborted, "execution lease superseded")
		}
		if run.Status == "succeeded" || run.Status == "failed" || run.Status == "stopped" {
			return status.Error(codes.FailedPrecondition, "run is terminal")
		}
		before := run.Stage
		if err := fn(tx, &run); err != nil {
			return err
		}
		run.Version++
		run.UpdatedAt = time.Now()
		if run.Stage != before {
			run.Steps = append(run.Steps, model.RunStep{Version: run.Version, Stage: run.Stage, CreatedAt: run.UpdatedAt.Unix()})
		}
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		return queueRun(tx, &run, "updated")
	})
	return &run, err
}
func (r *RunRepo) Trajectory(ctx context.Context, id string) ([]model.RunTrajectory, error) {
	var blocks []model.RunTrajectory
	err := r.DB.WithContext(ctx).Where("run_id = ?", id).Order("version ASC").Find(&blocks).Error
	return blocks, err
}
func AppendRunBlock(tx *gorm.DB, run *model.Run, kind string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	id, err := identity.New()
	if err != nil {
		return err
	}
	run.TrajectoryVersion++
	return tx.Create(&model.RunTrajectory{ID: id, RunID: run.ID, Version: run.TrajectoryVersion, Kind: kind, Block: raw}).Error
}
func QueueRunOutput(tx *gorm.DB, run *model.Run) error   { return queueRun(tx, run, "output") }
func QueueRunDelivery(tx *gorm.DB, run *model.Run) error { return queueRun(tx, run, "deliver") }

func (r *RunRepo) Claim(ctx context.Context, kinds []string) (*model.RunOutbox, error) {
	var item model.RunOutbox
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("kind IN ? AND completed_at IS NULL AND available_at <= ? AND (lease_until IS NULL OR lease_until < ?)", kinds, now, now).Order("created_at ASC,id ASC").First(&item).Error; err != nil {
			return err
		}
		token, err := identity.New()
		if err != nil {
			return err
		}
		until := now.Add(time.Minute)
		item.LeaseToken = &token
		item.LeaseUntil = &until
		item.Attempts++
		if item.Kind == "execute" {
			var claimed model.Run
			result := tx.Model(&claimed).Clauses(clause.Returning{Columns: []clause.Column{{Name: "execution_generation"}}}).Where("id = ? AND status IN ('accepted','running')", item.RunID).UpdateColumn("execution_generation", gorm.Expr("execution_generation + 1"))
			if result.Error != nil {
				return result.Error
			}
			item.ExecutionGeneration = claimed.ExecutionGeneration
		}
		return tx.Save(&item).Error
	})
	return &item, err
}
func (r *RunRepo) Renew(ctx context.Context, item *model.RunOutbox) error {
	result := r.DB.WithContext(ctx).Model(&model.RunOutbox{}).Where("id = ? AND lease_token = ? AND completed_at IS NULL", item.ID, item.LeaseToken).Update("lease_until", time.Now().Add(time.Minute))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return status.Error(codes.Aborted, "outbox lease lost")
	}
	return nil
}
func (r *RunRepo) Complete(ctx context.Context, item *model.RunOutbox) error {
	return r.DB.WithContext(ctx).Model(&model.RunOutbox{}).Where("id = ? AND lease_token = ?", item.ID, item.LeaseToken).Updates(map[string]any{"completed_at": time.Now(), "lease_until": nil, "lease_token": nil}).Error
}
func (r *RunRepo) Retry(ctx context.Context, item *model.RunOutbox) error {
	return r.DB.WithContext(ctx).Model(&model.RunOutbox{}).Where("id = ? AND lease_token = ?", item.ID, item.LeaseToken).Updates(map[string]any{"available_at": time.Now().Add(time.Duration(min(item.Attempts, 30)) * time.Second), "lease_until": nil, "lease_token": nil}).Error
}

// Report serializes executor and user commands with durable retry receipts.
// Authorization precedes receipt lookup, including terminal replay.
func (r *RunRepo) Report(ctx context.Context, id, key, digest string, authorize func(*model.Run) error, apply func(*gorm.DB, *model.Run) error) (*model.PublicRun, error) {
	var receipt model.PublicRun
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "Run not found")
			}
			return err
		}
		// Cross-domain source authorization takes its own binding lock. Complete
		// those RPCs before holding local participation locks, then fence revocation
		// and executor routing against the freshly locked authoritative rows.
		if err := authorize(&run); err != nil {
			return err
		}
		var binding model.ConvBot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", run.BindingID, run.BotID, run.ConvID).First(&binding).Error; err != nil {
			return status.Error(codes.PermissionDenied, "binding revoked")
		}
		var bot model.Bot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", run.BotID).First(&bot).Error; err != nil {
			return status.Error(codes.PermissionDenied, "Bot unavailable")
		}
		if run.ExecutorType == "third_party" {
			var config struct {
				ExecutorRevision int64  `json:"executor_revision"`
				ConnMode         string `json:"conn_mode"`
				CallbackURL      string `json:"callback_url"`
			}
			if err := json.Unmarshal(run.Config, &config); err != nil {
				return err
			}
			if bot.Type != "third_party" || bot.ExecutorRevision != config.ExecutorRevision || bot.ConnMode != config.ConnMode || bot.CallbackURL != config.CallbackURL {
				return status.Error(codes.PermissionDenied, "executor authorization or routing changed")
			}
		}
		var prior model.RunReceipt
		err := tx.Where("run_id = ? AND update_key = ?", id, key).First(&prior).Error
		if err == nil {
			if prior.ContentHash != digest {
				return status.Error(codes.AlreadyExists, "update key has different content")
			}
			return json.Unmarshal(prior.Receipt, &receipt)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if run.Status == "succeeded" || run.Status == "failed" || run.Status == "stopped" || run.OutputStatus == "pending" || run.OutputStatus == "generated" {
			return status.Error(codes.FailedPrecondition, "Run no longer accepts updates")
		}
		before := run.Stage
		if err := apply(tx, &run); err != nil {
			return err
		}
		run.Version++
		run.UpdatedAt = time.Now()
		if before != run.Stage {
			run.Steps = append(run.Steps, model.RunStep{Version: run.Version, Stage: run.Stage, CreatedAt: run.UpdatedAt.Unix()})
		}
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		if err := queueRun(tx, &run, "updated"); err != nil {
			return err
		}
		receipt = run.Public()
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		receiptID, err := identity.New()
		if err != nil {
			return err
		}
		return tx.Create(&model.RunReceipt{ID: receiptID, RunID: id, UpdateKey: key, ContentHash: digest, Receipt: raw}).Error
	})
	return &receipt, err
}
