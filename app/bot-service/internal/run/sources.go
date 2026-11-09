package run

import (
	"encoding/json"
	"slices"

	"github.com/maomeng/aim/app/bot-service/internal/model"
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
	if parent.MessageID != nil {
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
