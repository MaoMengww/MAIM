package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RunRepo struct{ DB *database.DB }

func NewRunRepo(db *database.DB) *RunRepo { return &RunRepo{DB: db} }

// All Run writes take the conversation barrier before work/participation locks.
// Source RPCs must finish before entering this transaction: messaging also takes
// the conversation lock and must never call back through a held Run lock.
func lockRunConversation(tx *gorm.DB, convID string) error {
	var conv struct{ ID string }
	return tx.Table("messaging.conversations").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", convID).Take(&conv).Error
}

func RunMember(tx *gorm.DB, convID, userID string) (string, int32, error) {
	var member struct {
		ID   string
		Role int32
	}
	err := tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", convID, userID).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = status.Error(codes.PermissionDenied, "current conversation membership required")
	}
	return member.ID, member.Role, err
}

func RunController(tx *gorm.DB, run *model.Run, user string) error {
	if run.Visibility == "personal" && (run.PersonalUserID == nil || *run.PersonalUserID != user) {
		return status.Error(codes.PermissionDenied, "personal Run control is restricted to requester")
	}
	member, role, err := RunMember(tx, run.ConvID, user)
	if err != nil {
		return err
	}
	if role <= 2 || (user == run.RequesterID && run.RequesterMemberID != nil && *run.RequesterMemberID == member) || (user == run.ControllerID && run.ControllerMemberID != nil && *run.ControllerMemberID == member) || run.ControlMemberships[user] == member {
		return nil
	}
	return status.Error(codes.PermissionDenied, "Run control requires current requester, controller, grant or administrator")
}

func lockRunAuthority(tx *gorm.DB, run *model.Run, controller bool) error {
	var binding model.ConvBot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", run.BindingID, run.BotID, run.ConvID).First(&binding).Error; err != nil {
		return status.Error(codes.PermissionDenied, "binding revoked")
	}
	if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
		return status.Error(codes.PermissionDenied, "participation permission revoked")
	}
	var bot model.Bot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", run.BotID).First(&bot).Error; err != nil {
		return status.Error(codes.PermissionDenied, "Bot unavailable")
	}
	if run.AccessRevoked || bot.AuthorizationVersion != run.BotAuthorizationVersion || binding.AuthorizationVersion != run.BindingAuthorizationVersion {
		return status.Error(codes.PermissionDenied, "captured authorization revoked")
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
			return status.Error(codes.PermissionDenied, "executor authorization or route changed")
		}
	}
	if err := resourcegrant.CheckIDs(tx, resourcegrant.Access{BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID, Visibility: run.Visibility, GrantIDs: run.ResourceGrantIDs}, run.Visibility != "personal", true); err != nil {
		return err
	}
	if err := CheckRunSources(tx, run); err != nil {
		return err
	}
	if controller {
		member, _, err := RunMember(tx, run.ConvID, run.ControllerID)
		if err != nil || run.ControllerMemberID == nil || *run.ControllerMemberID != member {
			return status.Error(codes.FailedPrecondition, "controller membership lost; explicit takeover required")
		}
	}
	return nil
}

type ReportAuthority struct {
	UserID           string
	Control          bool
	StopConfirmation bool
}

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
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND visibility = 'conversation'", convID).Order("created_at DESC,id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}
func queueRun(tx *gorm.DB, run *model.Run, kind string) error {
	if kind == "updated" && run.Visibility == "personal" {
		return nil
	}
	id, err := identity.New()
	if err != nil {
		return err
	}
	raw := json.RawMessage(`{}`)
	if kind == "updated" {
		public := run.Public()
		public.ResultText, public.ResultType, public.ResultContent, public.WaitingReason, public.Error = "", "", nil, "", ""
		public.Sources, public.Actions = []map[string]string{}, []model.PublicAction{}
		raw, err = json.Marshal(map[string]any{"type": "bot.run.updated", "conv_id": run.ConvID, "bot_id": run.BotID, "run": public})
		if err != nil {
			return err
		}
	}
	if kind == "deliver" {
		if run.ControlOperation == "stop" {
			// Revocation still permits a stop instruction, never another material copy.
			raw, err = json.Marshal(map[string]any{"type": "bot.run.request", "run_id": run.ID, "bot_id": run.BotID, "conv_id": run.ConvID, "binding_id": run.BindingID, "generation": run.ExecutionGeneration, "input_version": run.InputVersion, "operation": "stop", "reason": run.StopReason})
		} else {
			var inputs []model.RunInput
			if err := tx.Where("run_id = ?", run.ID).Order("version ASC").Find(&inputs).Error; err != nil {
				return err
			}
			ordered := make([]map[string]any, 0, len(inputs))
			for _, input := range inputs {
				ordered = append(ordered, map[string]any{"version": input.Version, "requester_id": input.RequesterID, "sender_type": "user", "source_message_id": input.SourceMessageID, "content": input.Content, "materials": input.Materials, "sources": input.Sources, "created_at": input.CreatedAt.Unix()})
			}
			raw, err = json.Marshal(map[string]any{"type": "bot.run.request", "run": run.Public(), "generation": run.ExecutionGeneration, "input_version": run.InputVersion, "inputs": ordered, "operation": run.ControlOperation})
		}
		if err != nil {
			return err
		}
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.RunOutbox{ID: id, RunID: run.ID, Kind: kind, Version: run.Version, Payload: raw, AvailableAt: time.Now()}).Error
}

// Accept serializes retry identity and writes the accepted input, work and all
// publication intents in one Bot-domain transaction.
func (r *RunRepo) Accept(ctx context.Context, run *model.Run, input string, freshMaterials json.RawMessage) (*model.Run, error) {
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunConversation(tx, run.ConvID); err != nil {
			return err
		}
		if err := lockRunAuthority(tx, run, true); err != nil {
			return err
		}
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
			member, _, err := RunMember(tx, prior.ConvID, prior.RequesterID)
			if err != nil {
				return err
			}
			if prior.RequesterMemberID == nil || *prior.RequesterMemberID != member {
				return status.Error(codes.PermissionDenied, "submission belongs to earlier membership")
			}
			if err := lockRunAuthority(tx, &prior, false); err != nil {
				return err
			}
			*run = prior
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := CheckAcceptedMessages(tx, run, freshMaterials); err != nil {
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
			if parent.Visibility != run.Visibility || (parent.Visibility == "personal" && (parent.PersonalUserID == nil || run.PersonalUserID == nil || *parent.PersonalUserID != *run.PersonalUserID)) {
				return status.Error(codes.PermissionDenied, "private interaction cannot transfer")
			}
			if !TerminalRun(&parent) {
				return status.Error(codes.FailedPrecondition, "continuation requires a terminal parent Run")
			}
			if err := lockRunAuthority(tx, &parent, false); err != nil {
				return err
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
			if err := tx.Create(&model.Interaction{ID: run.InteractionID, BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID, Status: "active", Visibility: run.Visibility, PersonalUserID: run.PersonalUserID}).Error; err != nil {
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
		acceptedEditCount := int32(0)
		var captured []struct {
			MessageID string `json:"message_id"`
			EditCount int32  `json:"edit_count"`
		}
		if err := json.Unmarshal(run.Materials, &captured); err != nil {
			return err
		}
		for _, source := range captured {
			if run.SourceMessageID != nil && source.MessageID == *run.SourceMessageID {
				acceptedEditCount = source.EditCount
			}
		}
		if err := tx.Create(&model.RunInput{ID: id, RunID: run.ID, Version: 1, RequesterID: run.RequesterID, SourceMessageID: run.SourceMessageID, AcceptedEditCount: acceptedEditCount, Provenance: run.Provenance, InputKey: run.InputKey, Content: input, ContentHash: run.InputHash, Materials: run.Materials, Sources: run.Sources}).Error; err != nil {
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
	return r.change(ctx, id, generation, true, fn)
}

func (r *RunRepo) ChangeLifecycle(ctx context.Context, id string, generation int64, fn func(*gorm.DB, *model.Run) error) (*model.Run, error) {
	return r.change(ctx, id, generation, false, fn)
}

func (r *RunRepo) change(ctx context.Context, id string, generation int64, execution bool, fn func(*gorm.DB, *model.Run) error) (*model.Run, error) {
	var run model.Run
	prior, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	err = r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunConversation(tx, prior.ConvID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; err != nil {
			return err
		}
		if generation > 0 && run.ExecutionGeneration != generation {
			return status.Error(codes.Aborted, "execution lease superseded")
		}
		if run.Status == "succeeded" || run.Status == "failed" || run.Status == "stopped" {
			return status.Error(codes.FailedPrecondition, "run is terminal")
		}
		if execution {
			if run.Status == "stopping" || run.Status == "waiting_authorization" {
				return status.Error(codes.FailedPrecondition, "Run execution is suspended")
			}
			if err := lockRunAuthority(tx, &run, true); err != nil {
				return err
			}
		}
		before := run.Stage
		if err := fn(tx, &run); err != nil {
			return err
		}
		run.Version++
		run.UpdatedAt = time.Now()
		if TerminalRun(&run) {
			run.StreamStatus = "closed"
		}
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
	provenance := run.Provenance
	var message struct {
		Role string `json:"role"`
	}
	if kind == "message" && json.Unmarshal(raw, &message) == nil && message.Role == "system" {
		provenance = json.RawMessage(`[]`)
	}
	run.TrajectoryVersion++
	return tx.Create(&model.RunTrajectory{ID: id, RunID: run.ID, Version: run.TrajectoryVersion, Kind: kind, Block: raw, Provenance: provenance}).Error
}

// ReconcileAuthorizations scrubs revoked resource copies even in terminal work.
// durable facts remain; only dependent copies are removed by the callback.
func (r *RunRepo) ReconcileAuthorizations(ctx context.Context, id string, version int64, apply func(*gorm.DB, *model.Run) error) (*model.Run, error) {
	prior, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var run model.Run
	err = r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunConversation(tx, prior.ConvID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; err != nil {
			return err
		}
		if run.Version != version {
			return status.Error(codes.Aborted, "Run changed during source reconciliation")
		}
		if err := apply(tx, &run); err != nil {
			return err
		}
		run.Version++
		run.UpdatedAt = time.Now()
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		return queueRun(tx, &run, "updated")
	})
	return &run, err
}

// CheckMaterialHistory checks the live grant against the frozen accepted seq,
// without requiring the original message to remain present or visible.
func CheckMaterialHistory(tx *gorm.DB, run *model.Run, grant string, seq int64) error {
	if seq <= 0 {
		return status.Error(codes.PermissionDenied, "history source has no accepted sequence")
	}
	var count int64
	if err := tx.Table("messaging.bot_history_grants g").
		Joins("JOIN messaging.conv_bots b ON b.id = g.binding_id").
		Joins("JOIN messaging.conv_members member ON member.conv_id = b.conv_id AND member.user_id = g.granted_by AND member.member_type = 'user'").
		Where("g.id = ? AND g.binding_id = ? AND b.conv_id = ? AND ? BETWEEN g.start_seq AND g.end_seq", grant, run.BindingID, run.ConvID, seq).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return status.Error(codes.PermissionDenied, "history source authorization revoked")
	}
	return nil
}

type acceptedMessage struct {
	MessageID      string `json:"message_id"`
	Seq            int64  `json:"seq"`
	EditCount      int32  `json:"edit_count"`
	HistoryGrantID string `json:"history_grant_id"`
	RequesterID    string `json:"requester_id"`
}

// CheckAcceptedMessages is used only when persisting newly observed messages.
// The conversation barrier prevents edit/hide/recall from racing acceptance;
// inherited work copies must never be supplied here.
func CheckAcceptedMessages(tx *gorm.DB, run *model.Run, raw json.RawMessage) error {
	var materials []acceptedMessage
	if err := json.Unmarshal(raw, &materials); err != nil {
		return err
	}
	for _, material := range materials {
		if material.MessageID == "" {
			continue
		}
		var message struct {
			Seq       int64
			EditCount int32
			Status    int32
		}
		if err := tx.Table("messaging.messages").Select("seq,edit_count,status").Where("id = ? AND conv_id = ?", material.MessageID, run.ConvID).Take(&message).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.PermissionDenied, "source is no longer visible")
			}
			return err
		}
		if message.Status == 2 {
			return status.Error(codes.PermissionDenied, "source is no longer visible")
		}
		if message.EditCount != material.EditCount || message.Seq != material.Seq {
			return status.Error(codes.Aborted, "source version changed during acceptance")
		}
		requester := material.RequesterID
		if requester == "" {
			requester = run.RequesterID
		}
		var hidden int64
		if err := tx.Table("messaging.personal_message_deletions").Where("message_id = ? AND conv_id = ? AND user_id = ?", material.MessageID, run.ConvID, requester).Count(&hidden).Error; err != nil {
			return err
		}
		if hidden != 0 {
			return status.Error(codes.PermissionDenied, "source is no longer visible")
		}
		if material.HistoryGrantID != "" {
			if err := CheckMaterialHistory(tx, run, material.HistoryGrantID, material.Seq); err != nil {
				return err
			}
			if err := tx.Table("messaging.personal_message_deletions d").Joins("JOIN messaging.bot_history_grants g ON g.granted_by = d.user_id").Where("g.id = ? AND d.conv_id = ? AND d.message_id = ?", material.HistoryGrantID, run.ConvID, material.MessageID).Count(&hidden).Error; err != nil {
				return err
			}
			if hidden != 0 {
				return status.Error(codes.PermissionDenied, "history source is no longer visible")
			}
		}
	}
	return nil
}

// CheckRunSources fences live authorization of accepted work. Message identity
// and accepted versions are provenance only, never ongoing IM visibility checks.
func CheckRunSources(tx *gorm.DB, run *model.Run) error {
	var versions []model.SourceVersion
	if len(run.Provenance) > 0 {
		if err := json.Unmarshal(run.Provenance, &versions); err != nil {
			return err
		}
	}
	for _, version := range versions {
		if version.ParentRunID == "" {
			continue
		}
		var parent model.Run
		if err := tx.Where("id = ?", version.ParentRunID).Take(&parent).Error; err != nil {
			return status.Error(codes.PermissionDenied, "parent source unavailable")
		}
		if parent.BranchVersion != version.SourceRevision || parent.AccessRevoked {
			return status.Error(codes.Aborted, "parent source validity changed")
		}
	}
	var materials []acceptedMessage
	if err := json.Unmarshal(run.Materials, &materials); err != nil {
		return err
	}
	for _, material := range materials {
		if material.HistoryGrantID != "" {
			if err := CheckMaterialHistory(tx, run, material.HistoryGrantID, material.Seq); err != nil {
				return err
			}
		}
	}
	return nil
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

// InputContents loads only the accepted body facts needed by a viewer projection.
func (r *RunRepo) InputContents(ctx context.Context, id string) ([]model.RunInput, error) {
	var inputs []model.RunInput
	err := r.DB.WithContext(ctx).Select("version", "content", "invalidated").Where("run_id = ?", id).Find(&inputs).Error
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
				if claimed.Stage != "action_decided" || item.Attempts != 1 {
					claimed.ExecutionGeneration++
					if err := tx.Model(&claimed).UpdateColumn("execution_generation", claimed.ExecutionGeneration).Error; err != nil {
						return err
					}
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

var errRunAuthorizationChanged = errors.New("Run changed during authorization")

// Report serializes executor and user commands with durable retry receipts.
// Authorization precedes receipt lookup, including terminal replay.
func (r *RunRepo) Report(ctx context.Context, id, key, digest string, authority ReportAuthority, authorize func(*model.Run) error, apply func(*gorm.DB, *model.Run) error) (*model.PublicRun, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		receipt, err := r.reportOnce(ctx, id, key, digest, authority, authorize, apply)
		if !errors.Is(err, errRunAuthorizationChanged) {
			return receipt, err
		}
		// No command was applied. Re-read and reauthorize the new version; never
		// retry rejected permissions, stale generations or conflicting input keys.
	}
}

func (r *RunRepo) reportOnce(ctx context.Context, id, key, digest string, authority ReportAuthority, authorize func(*model.Run) error, apply func(*gorm.DB, *model.Run) error) (*model.PublicRun, error) {
	var receipt model.PublicRun
	checked, err := r.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Error(codes.NotFound, "Run not found")
	}
	if err != nil {
		return nil, err
	}
	if err := authorize(checked); err != nil {
		return nil, err
	}
	err = r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunConversation(tx, checked.ConvID); err != nil {
			return err
		}
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
		if run.Version != checked.Version {
			return errRunAuthorizationChanged
		}
		if !authority.StopConfirmation {
			if err := lockRunAuthority(tx, &run, authority.UserID == ""); err != nil {
				return err
			}
		}
		if authority.UserID != "" {
			if run.Visibility == "personal" && (run.PersonalUserID == nil || *run.PersonalUserID != authority.UserID) {
				return status.Error(codes.PermissionDenied, "personal Run inaccessible")
			}
			if authority.Control {
				if err := RunController(tx, &run, authority.UserID); err != nil {
					return err
				}
			} else if _, _, err := RunMember(tx, run.ConvID, authority.UserID); err != nil {
				return err
			}
		}
		var prior model.RunReceipt
		err := tx.Where("run_id = ? AND update_key = ?", id, key).First(&prior).Error
		if err == nil {
			if prior.ContentHash != digest {
				return status.Error(codes.AlreadyExists, "update key has different content")
			}
			receipt = run.Public()
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if interaction.Status != "active" {
			return status.Error(codes.FailedPrecondition, "interaction is closed")
		}
		if TerminalRun(&run) {
			return status.Error(codes.FailedPrecondition, "Run no longer accepts updates")
		}
		before := run.Stage
		if err := apply(tx, &run); err != nil {
			return err
		}
		if before == "pending_action" && run.Stage != "pending_action" && run.Status != "stopping" && run.Status != "stopped" {
			return status.Error(codes.FailedPrecondition, "concrete action authorization required")
		}
		run.Version++
		run.UpdatedAt = time.Now()
		if TerminalRun(&run) {
			run.StreamStatus = "closed"
		}
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
func (r *RunRepo) CloseInteraction(ctx context.Context, id, key, digest, user string, authorize func(*model.Run) error) (*model.PublicRun, error) {
	var receipt model.PublicRun
	checked, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := authorize(checked); err != nil {
		return nil, err
	}
	err = r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunConversation(tx, checked.ConvID); err != nil {
			return err
		}
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
		if target.Version != checked.Version {
			return status.Error(codes.Aborted, "Run changed during authorization")
		}
		if err := RunController(tx, &target, user); err != nil {
			return err
		}
		if err := lockRunAuthority(tx, &target, false); err != nil {
			return err
		}
		var prior model.RunReceipt
		err := tx.Where("run_id = ? AND update_key = ?", id, key).First(&prior).Error
		if err == nil {
			if prior.ContentHash != digest {
				return status.Error(codes.AlreadyExists, "update key has different content")
			}
			receipt = target.Public()
			return nil
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
