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

func materialProvenance(materials []Material) (json.RawMessage, error) {
	versions := []model.SourceVersion{}
	for _, material := range materials {
		if material.MessageID != "" {
			versions = append(versions, model.SourceVersion{MessageID: material.MessageID, AcceptedEditCount: material.EditCount})
		}
		if material.FileID != "" {
			versions = append(versions, model.SourceVersion{FileID: material.FileID})
		}
		if material.KnowledgeID != "" {
			versions = append(versions, model.SourceVersion{KnowledgeID: material.KnowledgeID})
		}
		var inherited []model.SourceVersion
		if len(material.Provenance) > 0 {
			if err := json.Unmarshal(material.Provenance, &inherited); err != nil {
				return nil, err
			}
		}
		versions = append(versions, inherited...)
	}
	return json.Marshal(versions)
}

func inheritedProvenance(parent *model.Run) (json.RawMessage, error) {
	var versions []model.SourceVersion
	if len(parent.Provenance) > 0 {
		if err := json.Unmarshal(parent.Provenance, &versions); err != nil {
			return nil, err
		}
	}
	versions = append(versions, model.SourceVersion{ParentRunID: parent.ID, SourceRevision: parent.BranchVersion})
	if parent.MessageID != nil && !parent.ResultInvalidated {
		versions = append(versions, model.SourceVersion{MessageID: *parent.MessageID})
	}
	return json.Marshal(versions)
}

func mergeProvenance(a, b json.RawMessage) (json.RawMessage, error) {
	var left, right []model.SourceVersion
	if len(a) > 0 {
		if err := json.Unmarshal(a, &left); err != nil {
			return nil, err
		}
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &right); err != nil {
			return nil, err
		}
	}
	return json.Marshal(append(left, right...))
}

func dependent(raw json.RawMessage, invalid map[string]bool, parents map[string]bool) bool {
	var versions []model.SourceVersion
	if len(raw) > 0 && json.Unmarshal(raw, &versions) != nil {
		return true
	}
	for _, version := range versions {
		if invalid[version.MessageID] || invalid["file:"+version.FileID] || invalid["knowledge:"+version.KnowledgeID] || parents[version.ParentRunID] {
			return true
		}
	}
	return false
}

// Revalidation is synchronous and reconstructs validity from current durable rows.
// A personal overlay on shared material is a viewer boundary, never a work stop.
func (s *Service) refreshSources(ctx context.Context, r *model.Run) error {
	var materials []Material
	if err := json.Unmarshal(r.Materials, &materials); err != nil {
		return err
	}
	var versions []model.SourceVersion
	if len(r.Provenance) > 0 {
		if err := json.Unmarshal(r.Provenance, &versions); err != nil {
			return err
		}
	}
	if len(versions) == 0 {
		raw, err := materialProvenance(materials)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &versions); err != nil {
			return err
		}
	}
	runVersions := versions
	// Frozen action sources survive material scrubbing and later Run result resets.
	// Include them in revalidation so legacy terminal receipts are also repaired.
	var actions []model.RunAction
	if err := s.svc.DB.WithContext(ctx).Where("run_id = ? AND source_invalidated = false", r.ID).Find(&actions).Error; err != nil {
		return err
	}
	for _, action := range actions {
		var sources []model.SourceVersion
		if err := json.Unmarshal(action.Provenance, &sources); err != nil {
			return err
		}
		versions = append(versions, sources...)
	}
	changes := []model.SourceChange{}
	invalid, parents := map[string]bool{}, map[string]bool{}
	seen := map[model.SourceVersion]bool{}
	for _, version := range versions {
		if seen[version] {
			continue
		}
		seen[version] = true
		if version.ParentRunID != "" {
			parent, err := s.repo.Get(ctx, version.ParentRunID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				parents[version.ParentRunID] = true
				continue
			}
			if err != nil {
				return err
			}
			if err := s.refreshSources(ctx, parent); err != nil {
				return err
			}
			if parent.BranchVersion != version.SourceRevision || parent.AccessRevoked {
				parents[parent.ID] = true
			}
			continue
		}
		if version.FileID != "" || version.KnowledgeID != "" {
			kind, id := "file", version.FileID
			if id == "" {
				kind, id = "knowledge", version.KnowledgeID
			}
			if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), resourceAccess(r), kind, id, r.Visibility != "personal", false); err != nil {
				if status.Code(err) != codes.PermissionDenied {
					return err
				}
				invalid[kind+":"+id] = true
			}
			continue
		}
		if version.MessageID == "" {
			continue
		}
		var message struct {
			ID        string
			EditCount int32
			Status    int32
		}
		err := s.svc.DB.WithContext(ctx).Table("messaging.messages").Select("id, edit_count, status").Where("id = ? AND conv_id = ?", version.MessageID, r.ConvID).Take(&message).Error
		change := model.SourceChange{MessageID: version.MessageID, AcceptedEditCount: version.AcceptedEditCount, CurrentEditCount: message.EditCount}
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && message.Status == 2) {
			change.Status = "withdrawn"
			invalid[version.MessageID] = true
		} else if err != nil {
			return err
		} else if message.EditCount != version.AcceptedEditCount {
			change.Status = "modified"
		}
		if change.Status != "" {
			changes = append(changes, change)
		}
		for _, prior := range r.SourceChanges {
			if prior.MessageID == version.MessageID && prior.AcceptedEditCount == version.AcceptedEditCount && prior.Status == "unavailable" {
				invalid[version.MessageID] = true
				changes = slices.DeleteFunc(changes, func(c model.SourceChange) bool {
					return c.MessageID == version.MessageID && c.AcceptedEditCount == version.AcceptedEditCount
				})
				changes = append(changes, prior)
			}
		}
	}
	// Historical grants and private sources retain their original principal.
	for _, material := range materials {
		if material.MessageID == "" || invalid[material.MessageID] || (material.Shared && material.HistoryGrantID == "") {
			continue
		}
		input := Input{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: material.RequesterID, Visibility: r.Visibility, ResourceGrantIDs: r.ResourceGrantIDs}
		if input.RequesterID == "" {
			input.RequesterID = r.RequesterID
		}
		_, err := s.source(ctx, input, material.MessageID, material.Kind != "request_source" || material.SourceExplicit)
		if err == nil && material.HistoryGrantID != "" {
			err = repo.CheckMaterialHistory(s.svc.DB.WithContext(ctx), r, material.HistoryGrantID, material.MessageID)
		}
		if err != nil {
			if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.NotFound {
				return err
			}
			invalid[material.MessageID] = true
			changes = slices.DeleteFunc(changes, func(c model.SourceChange) bool { return c.MessageID == material.MessageID })
			changes = append(changes, model.SourceChange{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, CurrentEditCount: material.EditCount, Status: "unavailable"})
		}
	}
	slices.SortFunc(changes, func(a, b model.SourceChange) int {
		if a.MessageID < b.MessageID {
			return -1
		}
		if a.MessageID > b.MessageID {
			return 1
		}
		return int(a.AcceptedEditCount - b.AcceptedEditCount)
	})
	changed := !slices.Equal(changes, r.SourceChanges)
	needsScrub := false
	for _, material := range materials {
		if invalid[material.MessageID] || parents[material.ParentRunID] || dependent(material.Provenance, invalid, parents) {
			needsScrub = true
		}
	}
	removeMaterial := func(m Material) bool {
		return invalid[m.MessageID] || invalid["file:"+m.FileID] || invalid["knowledge:"+m.KnowledgeID] || parents[m.ParentRunID] || dependent(m.Provenance, invalid, parents)
	}
	for _, material := range materials {
		if removeMaterial(material) {
			needsScrub = true
		}
	}
	var acceptedInputs []model.RunInput
	if err := s.svc.DB.WithContext(ctx).Where("run_id = ? AND invalidated = false AND content <> ''", r.ID).Find(&acceptedInputs).Error; err != nil {
		return err
	}
	for _, input := range acceptedInputs {
		if input.SourceMessageID != nil && invalid[*input.SourceMessageID] {
			needsScrub = true
		}
	}
	for _, action := range actions {
		if dependent(action.Provenance, invalid, parents) {
			needsScrub = true
		}
	}
	if !changed && !needsScrub {
		return nil
	}
	current, err := s.repo.ReconcileSources(ctx, r.ID, r.Version, func(tx *gorm.DB, current *model.Run) error {
		current.SourceChanges = changes
		current.SourceRevision++
		if needsScrub {
			current.ResultInvalidated = true
			var actions []model.RunAction
			if err := tx.Where("run_id = ? AND source_invalidated = false", current.ID).Find(&actions).Error; err != nil {
				return err
			}
			for _, action := range actions {
				if dependent(action.Provenance, invalid, parents) {
					// Keep execution/claim/completion facts; never roll back an external
					// action or turn its outcome into permission to replay old input.
					if err := tx.Model(&action).Updates(map[string]any{
						"source_invalidated": true, "arguments": json.RawMessage(`{}`),
						"target": "", "result": "",
					}).Error; err != nil {
						return err
					}
				}
			}
			current.BranchVersion++
			retained := slices.DeleteFunc(materials, removeMaterial)
			var err error
			current.Materials, err = json.Marshal(retained)
			if err != nil {
				return err
			}
			retainedVersions := slices.DeleteFunc(slices.Clone(runVersions), func(v model.SourceVersion) bool { return parents[v.ParentRunID] })
			current.Provenance, err = json.Marshal(retainedVersions)
			if err != nil {
				return err
			}
			var sources []map[string]any
			if err := json.Unmarshal(current.Sources, &sources); err != nil {
				return err
			}
			sources = slices.DeleteFunc(sources, func(source map[string]any) bool {
				id, _ := source["message_id"].(string)
				parent, _ := source["parent_run_id"].(string)
				file, _ := source["file_id"].(string)
				kb, _ := source["kb_id"].(string)
				return invalid[id] || parents[parent] || invalid["file:"+file] || invalid["knowledge:"+kb]
			})
			current.Sources, err = json.Marshal(sources)
			if err != nil {
				return err
			}
			var inputs []model.RunInput
			if err := tx.Where("run_id = ?", current.ID).Find(&inputs).Error; err != nil {
				return err
			}
			for _, input := range inputs {
				var own []Material
				if err := json.Unmarshal(input.Materials, &own); err != nil {
					return err
				}
				own = slices.DeleteFunc(own, removeMaterial)
				raw, err := json.Marshal(own)
				if err != nil {
					return err
				}
				var ownSources []map[string]any
				if err := json.Unmarshal(input.Sources, &ownSources); err != nil {
					return err
				}
				ownSources = slices.DeleteFunc(ownSources, func(source map[string]any) bool {
					id, _ := source["message_id"].(string)
					parent, _ := source["parent_run_id"].(string)
					file, _ := source["file_id"].(string)
					kb, _ := source["kb_id"].(string)
					return invalid[id] || parents[parent] || invalid["file:"+file] || invalid["knowledge:"+kb]
				})
				sourceRaw, err := json.Marshal(ownSources)
				if err != nil {
					return err
				}
				updates := map[string]any{"materials": raw, "sources": sourceRaw}
				if input.SourceMessageID != nil && invalid[*input.SourceMessageID] {
					updates["content"], updates["invalidated"] = "", true
				}
				if err := tx.Model(&model.RunInput{}).Where("id = ?", input.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
			var blocks []model.RunTrajectory
			if err := tx.Where("run_id = ? AND invalidated = false", current.ID).Find(&blocks).Error; err != nil {
				return err
			}
			for _, block := range blocks {
				if dependent(block.Provenance, invalid, parents) {
					if err := tx.Model(&block).Updates(map[string]any{"block": json.RawMessage(`{}`), "invalidated": true}).Error; err != nil {
						return err
					}
				}
			}
			if err := tx.Where("run_id = ?", current.ID).Delete(&model.RunAgentCheckpoint{}).Error; err != nil {
				return err
			}
			var calls []model.RunModelCall
			if err := tx.Where("run_id = ? AND invalidated = false", current.ID).Find(&calls).Error; err != nil {
				return err
			}
			for _, call := range calls {
				if dependent(call.Provenance, invalid, parents) || call.Status == "requested" {
					if err := tx.Model(&call).Updates(map[string]any{"request": json.RawMessage(`{}`), "response": json.RawMessage(`{}`), "invalidated": true, "status": "invalidated"}).Error; err != nil {
						return err
					}
				}
			}
			if err := tx.Model(&model.RunReceipt{}).Where("run_id = ?", current.ID).Update("receipt", json.RawMessage(`{}`)).Error; err != nil {
				return err
			}
			current.ContextNotice = "Unavailable source material and dependent context were removed; submit explicit input to reconfirm the remaining constraints."
			if !repo.TerminalRun(current) {
				current.ExecutionGeneration++
				if current.SourceMessageID != nil && invalid[*current.SourceMessageID] {
					if err := requestRunStop(tx, current, "request_source_recalled"); err != nil {
						return err
					}
				} else {
					if current.ExecutorType == "third_party" {
						current.ExecutorSequence, current.ExecutorStatus, current.DeliveryStatus = 0, "pending", "pending"
					}
					current.Status, current.Stage, current.WaitingReason = "waiting_input", "source_confirmation_required", "Source material is no longer available; explicit input is required."
					current.StreamStatus = "open"
					current.StopStatus = "none"
					current.ResultText, current.ResultType, current.ResultContent, current.OutputStatus = "", "", nil, "none"
				}
			}
		}
		if !needsScrub {
			var pending int64
			if err := tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested' AND checkpoint_id IS NOT NULL", current.ID).Count(&pending).Error; err != nil {
				return err
			}
			if pending > 0 && !repo.TerminalRun(current) {
				current.ExecutionGeneration++
				current.Status, current.Stage, current.WaitingReason = "waiting_input", "source_confirmation_required", "Source versions changed; submit explicit input to reconfirm the accepted constraints."
				current.StreamStatus = "open"
				if err := tx.Where("run_id = ?", current.ID).Delete(&model.RunAgentCheckpoint{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested' AND checkpoint_id IS NOT NULL", current.ID).Updates(map[string]any{"request": json.RawMessage(`{}`), "response": json.RawMessage(`{}`), "invalidated": true, "status": "invalidated"}).Error; err != nil {
				return err
			}
		}
		// Source edits keep accepted facts but reject an in-flight stale summary.
		if err := tx.Table("bot.run_compaction_checkpoints").Where("run_id = ? AND status <> 'invalid'", current.ID).Updates(map[string]any{"status": "invalid", "summary": json.RawMessage(`{}`), "source_versions": json.RawMessage(`[]`), "pinned_messages": json.RawMessage(`[]`)}).Error; err != nil {
			return err
		}
		if err := tx.Table("bot.run_compaction_calls").Where("run_id = ?", current.ID).Updates(map[string]any{"status": "invalidated", "request": json.RawMessage(`{}`), "response": json.RawMessage(`{}`)}).Error; err != nil {
			return err
		}
		kinds := []string{"updated"}
		if needsScrub {
			kinds = append(kinds, "deliver", "output")
		}
		if err := tx.Model(&model.RunOutbox{}).Where("run_id = ? AND kind IN ? AND completed_at IS NULL", current.ID, kinds).Updates(map[string]any{"payload": json.RawMessage(`{}`), "completed_at": time.Now()}).Error; err != nil {
			return err
		}
		return repo.CheckRunSources(tx, current)
	})
	if status.Code(err) == codes.Aborted {
		fresh, e := s.repo.Get(ctx, r.ID)
		if e != nil {
			return e
		}
		*r = *fresh
		return s.refreshSources(ctx, r)
	}
	if err == nil {
		*r = *current
	}
	return err
}

func (s *Service) recheckSources(ctx context.Context, tx *gorm.DB, r *model.Run) error {
	return repo.CheckRunSources(tx.WithContext(ctx), r)
}
