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

// Revalidate only live authorizations. Accepted message bodies and versions are
// durable work facts; their original IM rows never participate in this boundary.
func (s *Service) refreshAuthorizations(ctx context.Context, r *model.Run) error {
	return s.refreshAuthorizationSources(ctx, r, make(map[string]bool))
}

func (s *Service) refreshAuthorizationSources(ctx context.Context, r *model.Run, visiting map[string]bool) error {
	if visiting[r.ID] {
		return status.Error(codes.PermissionDenied, "Run source ancestry is cyclic")
	}
	visiting[r.ID] = true
	defer delete(visiting, r.ID)
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
			if err := s.refreshAuthorizationSources(ctx, parent, visiting); err != nil {
				return err
			}
			authority := *parent
			if repo.TerminalRun(parent) && sharedMaterials(parent) {
				member, _, err := repo.RunMember(s.svc.DB.WithContext(ctx), r.ConvID, r.ControllerID)
				if err != nil {
					return err
				}
				authority.ControllerID, authority.ControllerMemberID = r.ControllerID, &member
			}
			if reason, err := s.authorityState(ctx, &authority); err != nil {
				return err
			} else if reason != "" {
				parents[parent.ID] = true
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
	}
	// A frozen sequence proves the accepted material remains inside its live grant.
	for _, material := range materials {
		if material.HistoryGrantID == "" {
			continue
		}
		if err := repo.CheckMaterialHistory(s.svc.DB.WithContext(ctx), r, material.HistoryGrantID, material.Seq); err != nil {
			if status.Code(err) != codes.PermissionDenied {
				return err
			}
			invalid[material.MessageID] = true
		}
	}
	needsScrub := false
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
	if !needsScrub {
		return nil
	}
	current, err := s.repo.ReconcileAuthorizations(ctx, r.ID, r.Version, func(tx *gorm.DB, current *model.Run) error {
		current.SourceRevision++
		current.AccessRevoked, current.StopReason = true, "source_authorization_revoked"
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
		current.ContextNotice = "Revoked source authorization and dependent context were removed."
		if !repo.TerminalRun(current) {
			if current.ExecutorType == "builtin" {
				current.ExecutionGeneration++
			}
			if err := requestRunStop(tx, current, "source_authorization_revoked"); err != nil {
				return err
			}
		}
		// Real revocation invalidates summaries and framework recovery state.
		if err := tx.Table("bot.run_compaction_checkpoints").Where("run_id = ? AND status <> 'invalid'", current.ID).Updates(map[string]any{"status": "invalid", "summary": json.RawMessage(`{}`), "source_versions": json.RawMessage(`[]`), "pinned_messages": json.RawMessage(`[]`)}).Error; err != nil {
			return err
		}
		if err := tx.Table("bot.run_compaction_calls").Where("run_id = ?", current.ID).Updates(map[string]any{"status": "invalidated", "request": json.RawMessage(`{}`), "response": json.RawMessage(`{}`)}).Error; err != nil {
			return err
		}
		kinds := []string{"updated", "deliver", "output"}
		if err := tx.Model(&model.RunOutbox{}).Where("run_id = ? AND version <= ? AND kind IN ? AND completed_at IS NULL", current.ID, current.Version, kinds).Updates(map[string]any{"payload": json.RawMessage(`{}`), "completed_at": time.Now()}).Error; err != nil {
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
		delete(visiting, r.ID)
		return s.refreshAuthorizationSources(ctx, r, visiting)
	}
	if err == nil {
		*r = *current
	}
	return err
}

func (s *Service) recheckAuthorizations(ctx context.Context, tx *gorm.DB, r *model.Run) error {
	return repo.CheckRunSources(tx.WithContext(ctx), r)
}
