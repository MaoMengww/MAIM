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
		var inherited []model.SourceVersion
		if len(material.Provenance) > 0 {
			if err := json.Unmarshal(material.Provenance, &inherited); err != nil {
				return nil, err
			}
		}
		if material.KnowledgeID != "" && !slices.ContainsFunc(inherited, func(source model.SourceVersion) bool {
			return source.KnowledgeID == material.KnowledgeID && (source.KnowledgeChunkID != "" || source.KnowledgeMetadataHash != "")
		}) {
			versions = append(versions, model.SourceVersion{KnowledgeID: material.KnowledgeID})
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

func sourceInvalid(version model.SourceVersion, invalid map[string]bool, parents map[string]bool) bool {
	return invalid[version.MessageID] || ((version.HistoryGrantID != "" || version.HistorySeq != 0) && invalid["history:"+version.HistoryGrantID+":"+version.MessageID]) || invalid["file:"+version.FileID] || invalid["knowledge:"+version.KnowledgeID] || invalid["knowledge_doc:"+version.KnowledgeDocID] || invalid["knowledge_chunk:"+version.KnowledgeChunkID] || parents[version.ParentRunID]
}

func dependent(raw json.RawMessage, invalid map[string]bool, parents map[string]bool) bool {
	var versions []model.SourceVersion
	if len(raw) > 0 && json.Unmarshal(raw, &versions) != nil {
		return true
	}
	for _, version := range versions {
		if sourceInvalid(version, invalid, parents) {
			return true
		}
	}
	return false
}

// Explicit accepted messages remain durable facts. On-demand history and
// knowledge evidence also retain live source and sharing authorization fences.
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
	// A legacy knowledge copy without the accepted chunk version cannot prove
	// that its evidence survived replacement. Never mint a new version for it.
	for _, material := range materials {
		if material.Kind == "conversation_history" {
			if material.HistoryGrantID == "" || material.Seq <= 0 || material.MessageID == "" {
				invalid["history:"+material.HistoryGrantID+":"+material.MessageID] = true
				invalid["history_message:"+material.MessageID] = true
			}
			versions = append(versions, model.SourceVersion{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, HistoryGrantID: material.HistoryGrantID, HistorySeq: material.Seq})
		}
		if material.Kind != "knowledge" || material.KnowledgeID == "" {
			continue
		}
		var accepted []model.SourceVersion
		if len(material.Provenance) > 0 {
			if err := json.Unmarshal(material.Provenance, &accepted); err != nil {
				return err
			}
		}
		if !slices.ContainsFunc(accepted, func(source model.SourceVersion) bool {
			return source.KnowledgeID == material.KnowledgeID && source.KnowledgeChunkID != "" && source.KnowledgeContentHash != "" && source.KnowledgeDocumentHash != ""
		}) {
			invalid["knowledge:"+material.KnowledgeID] = true
		}
	}
	seen := map[model.SourceVersion]bool{}
	for _, version := range versions {
		if seen[version] {
			continue
		}
		seen[version] = true
		if version.HistoryGrantID != "" || version.HistorySeq != 0 {
			if err := resourcegrant.CheckHistorySource(s.svc.DB.WithContext(ctx), resourceAccess(r), version.HistorySource(), false); err != nil {
				if status.Code(err) != codes.PermissionDenied {
					return err
				}
				invalid["history:"+version.HistoryGrantID+":"+version.MessageID] = true
				invalid["history_message:"+version.MessageID] = true
			}
		}
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
			if version.KnowledgeID != "" {
				var bot model.Bot
				if err := s.svc.DB.WithContext(ctx).Where("id = ? AND enable_knowledge = true", r.BotID).Take(&bot).Error; err != nil {
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					invalid["knowledge:"+version.KnowledgeID] = true
				}
				if err := resourcegrant.CheckKnowledgeSource(s.svc.DB.WithContext(ctx), version.KnowledgeSource(), false); err != nil {
					if status.Code(err) != codes.PermissionDenied {
						return err
					}
					if version.KnowledgeMetadataHash != "" {
						invalid["knowledge:"+version.KnowledgeID] = true
					} else if version.KnowledgeChunkID == "" && version.KnowledgeDocID != "" {
						invalid["knowledge_doc:"+version.KnowledgeDocID] = true
					} else {
						invalid["knowledge_chunk:"+version.KnowledgeChunkID] = true
					}
				}
			}
			continue
		}
	}
	// Original accepted history snapshots retain their frozen-message semantics.
	for _, material := range materials {
		if material.HistoryGrantID == "" || material.Kind == "conversation_history" {
			continue
		}
		if err := repo.CheckMaterialHistory(s.svc.DB.WithContext(ctx), r, material.HistoryGrantID, material.Seq); err != nil {
			if status.Code(err) != codes.PermissionDenied {
				return err
			}
			invalid[material.MessageID] = true
		}
	}
	needsScrub := dependent(r.Provenance, invalid, parents)
	removeMaterial := func(m Material) bool {
		return invalid[m.MessageID] || (m.Kind == "conversation_history" && invalid["history:"+m.HistoryGrantID+":"+m.MessageID]) || invalid["file:"+m.FileID] || invalid["knowledge:"+m.KnowledgeID] || parents[m.ParentRunID] || dependent(m.Provenance, invalid, parents)
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
		current.ResultText, current.ResultType, current.ResultContent = "", "", nil
		current.Error = ""
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
		retainedVersions := slices.DeleteFunc(slices.Clone(runVersions), func(v model.SourceVersion) bool {
			return sourceInvalid(v, invalid, parents)
		})
		current.Provenance, err = json.Marshal(retainedVersions)
		if err != nil {
			return err
		}
		var sources []map[string]any
		if err := json.Unmarshal(current.Sources, &sources); err != nil {
			return err
		}
		removeSource := func(source map[string]any) bool {
			id, _ := source["message_id"].(string)
			parent, _ := source["parent_run_id"].(string)
			file, _ := source["file_id"].(string)
			kb, _ := source["kb_id"].(string)
			chunk, _ := source["chunk_id"].(string)
			doc, _ := source["doc_id"].(string)
			matched, _ := source["matched_chunk_id"].(string)
			context, _ := source["context_chunk_id"].(string)
			return invalid[id] || invalid["history_message:"+id] || parents[parent] || invalid["file:"+file] || invalid["knowledge:"+kb] || invalid["knowledge_doc:"+doc] || invalid["knowledge_chunk:"+chunk] || invalid["knowledge_chunk:"+matched] || invalid["knowledge_chunk:"+context]
		}
		sources = slices.DeleteFunc(sources, removeSource)
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
			ownSources = slices.DeleteFunc(ownSources, removeSource)
			sourceRaw, err := json.Marshal(ownSources)
			if err != nil {
				return err
			}
			updates := map[string]any{"materials": raw, "sources": sourceRaw}
			var ownVersions []model.SourceVersion
			if len(input.Provenance) > 0 {
				if err := json.Unmarshal(input.Provenance, &ownVersions); err != nil {
					return err
				}
			}
			ownVersions = slices.DeleteFunc(ownVersions, func(source model.SourceVersion) bool {
				return sourceInvalid(source, invalid, parents)
			})
			provenanceRaw, err := json.Marshal(ownVersions)
			if err != nil {
				return err
			}
			updates["provenance"] = provenanceRaw
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
