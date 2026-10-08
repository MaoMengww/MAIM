package repo

import (
	"context"
	"encoding/json"
	"errors"
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
		ordered := make([]map[string]any, 0, len(inputs))
		for _, input := range inputs {
			ordered = append(ordered, map[string]any{"version": input.Version, "requester_id": input.RequesterID, "sender_type": "user", "source_message_id": input.SourceMessageID, "content": input.Content, "materials": input.Materials, "sources": input.Sources, "created_at": input.CreatedAt.Unix()})
		}
		raw, err = json.Marshal(map[string]any{"type": "bot.run.request", "run": run.Public(), "generation": run.ExecutionGeneration, "input_version": run.InputVersion, "inputs": ordered, "operation": run.ControlOperation})
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
		if run.ParentRunID != nil {
			var interaction model.Interaction
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ?", run.InteractionID, run.BotID, run.ConvID, run.BindingID).First(&interaction).Error; err != nil {
				return status.Error(codes.PermissionDenied, "parent interaction is unavailable")
			}
			if interaction.Status != "active" {
				return status.Error(codes.FailedPrecondition, "interaction is closed; start a new request")
			}
			var parent model.Run
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND interaction_id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ?", *run.ParentRunID, run.InteractionID, run.BotID, run.ConvID, run.BindingID).First(&parent).Error; err != nil {
				return status.Error(codes.PermissionDenied, "parent Run is unavailable")
			}
			if !TerminalRun(&parent) {
				return status.Error(codes.FailedPrecondition, "continuation requires a terminal parent Run")
			}
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
		if run.ParentRunID == nil {
			if err := tx.Create(&model.Interaction{ID: run.InteractionID, BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID, Status: "active"}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.RunInput{ID: id, RunID: run.ID, Version: 1, RequesterID: run.RequesterID, SourceMessageID: run.SourceMessageID, InputKey: run.InputKey, Content: input, ContentHash: run.InputHash, Materials: run.Materials, Sources: run.Sources}).Error; err != nil {
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
func QueueRunOutput(tx *gorm.DB, run *model.Run) error    { return queueRun(tx, run, "output") }
func QueueRunDelivery(tx *gorm.DB, run *model.Run) error  { return queueRun(tx, run, "deliver") }
func QueueRunExecution(tx *gorm.DB, run *model.Run) error { return queueRun(tx, run, "execute") }

func TerminalRun(run *model.Run) bool {
	return run.Status == "succeeded" || run.Status == "failed" || run.Status == "stopped"
}

func (r *RunRepo) Inputs(ctx context.Context, id string) ([]model.RunInput, error) {
	var inputs []model.RunInput
	err := r.DB.WithContext(ctx).Where("run_id = ?", id).Order("version ASC").Find(&inputs).Error
	return inputs, err
}

func (r *RunRepo) FindResult(ctx context.Context, bot, conv, binding, message string) (*model.Run, error) {
	var run model.Run
	err := r.DB.WithContext(ctx).Where("bot_id = ? AND conv_id = ? AND binding_id = ? AND message_id = ?", bot, conv, binding, message).First(&run).Error
	return &run, err
}

func (r *RunRepo) Claim(ctx context.Context, kinds []string) (*model.RunOutbox, error) {
	var item model.RunOutbox
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("kind IN ? AND completed_at IS NULL AND available_at <= ? AND (lease_until IS NULL OR lease_until < ?)", kinds, now, now).Where("kind <> 'execute' OR NOT EXISTS (SELECT 1 FROM bot.run_outbox live WHERE live.run_id = run_outbox.run_id AND live.id <> run_outbox.id AND live.kind = 'execute' AND live.completed_at IS NULL AND live.lease_until > ?)", now).Order("created_at ASC,id ASC").First(&item).Error; err != nil {
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
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", item.RunID).First(&claimed).Error; err != nil {
				return err
			}
			// Recheck after the Run lock: concurrent claim queries may have both
			// observed the same pre-commit absence of a live execution lease.
			var live int64
			if err := tx.Model(&model.RunOutbox{}).Where("run_id = ? AND id <> ? AND kind = 'execute' AND completed_at IS NULL AND lease_until > ?", item.RunID, item.ID, now).Count(&live).Error; err != nil {
				return err
			}
			if live > 0 {
				return gorm.ErrRecordNotFound
			}
			if claimed.Status == "accepted" || claimed.Status == "running" {
				claimed.ExecutionGeneration++
				if err := tx.Model(&claimed).UpdateColumn("execution_generation", claimed.ExecutionGeneration).Error; err != nil {
					return err
				}
				item.ExecutionGeneration = claimed.ExecutionGeneration
			}
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
		if err := tx.Where("id = ?", id).First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "Run not found")
			}
			return err
		}
		var interaction model.Interaction
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", run.InteractionID).First(&interaction).Error; err != nil {
			return err
		}
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
		if interaction.Status != "active" {
			return status.Error(codes.FailedPrecondition, "interaction is closed")
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

// CloseInteraction takes the interaction lock before any Run lock, as Accept
// and Report do. It cuts continuation atomically without deleting work facts.
func (r *RunRepo) CloseInteraction(ctx context.Context, id, key, digest string, authorize func(*model.Run) error) (*model.PublicRun, error) {
	var receipt model.PublicRun
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.Run
		if err := tx.Where("id = ?", id).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "Run not found")
			}
			return err
		}
		var interaction model.Interaction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", target.InteractionID).First(&interaction).Error; err != nil {
			return err
		}
		var runs []model.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("interaction_id = ?", interaction.ID).Order("id ASC").Find(&runs).Error; err != nil {
			return err
		}
		for _, run := range runs {
			if run.ID == id {
				target = run
			}
		}
		if err := authorize(&target); err != nil {
			return err
		}
		var binding model.ConvBot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", target.BindingID, target.BotID, target.ConvID).First(&binding).Error; err != nil {
			return status.Error(codes.PermissionDenied, "binding revoked")
		}
		var bot model.Bot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", target.BotID).First(&bot).Error; err != nil {
			return status.Error(codes.PermissionDenied, "Bot unavailable")
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
		for _, run := range runs {
			if !TerminalRun(&run) {
				return status.Error(codes.FailedPrecondition, "finish every active Run before closing the interaction")
			}
		}
		now := time.Now()
		if interaction.Status != "closed" {
			interaction.Status, interaction.ClosedAt = "closed", &now
			if err := tx.Save(&interaction).Error; err != nil {
				return err
			}
			for i := range runs {
				run := &runs[i]
				run.InteractionStatus, run.UpdatedAt = "closed", now
				run.Version++
				if err := tx.Save(run).Error; err != nil {
					return err
				}
				if err := queueRun(tx, run, "updated"); err != nil {
					return err
				}
				if run.ID == id {
					target = *run
				}
			}
		}
		receipt = target.Public()
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
