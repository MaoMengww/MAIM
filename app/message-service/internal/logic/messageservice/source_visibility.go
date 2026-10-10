package messageservicelogic

import (
	"encoding/json"

	"gorm.io/gorm"
)

// captureMessageSources uses platform-owned Run provenance, never executor
// metadata. Published parent identities are sources even when later reconciliation
// scrubbed the parent's material copies or its message was globally deleted.
func captureMessageSources(tx *gorm.DB, convID string, provenance json.RawMessage, sourceMessageID, parentRunID, replyToID *string) ([]string, error) {
	var versions []struct {
		MessageID   string `json:"message_id"`
		ParentRunID string `json:"parent_run_id"`
	}
	if len(provenance) != 0 {
		if err := json.Unmarshal(provenance, &versions); err != nil {
			return nil, err
		}
	}
	var sources, parents []string
	for _, version := range versions {
		if version.MessageID != "" {
			sources = append(sources, version.MessageID)
		}
		if version.ParentRunID != "" {
			parents = append(parents, version.ParentRunID)
		}
	}
	if sourceMessageID != nil {
		sources = append(sources, *sourceMessageID)
	}
	if replyToID != nil {
		sources = append(sources, *replyToID)
	}
	if parentRunID != nil {
		parents = append(parents, *parentRunID)
	}
	if len(parents) == 0 {
		return sources, nil
	}
	published, err := captureParentOutputs(tx, convID, parents)
	if err != nil {
		return nil, err
	}
	return append(sources, published...), nil
}

func captureParentOutputs(tx *gorm.DB, convID string, parentIDs []string) ([]string, error) {
	var ids []string
	err := tx.Raw(`WITH RECURSIVE parents(id) AS (
	  SELECT id FROM bot.runs WHERE conv_id = ? AND id IN ?
	  UNION
	  SELECT ancestor.id FROM parents
	    JOIN bot.runs parent ON parent.id = parents.id
	    CROSS JOIN LATERAL (
	      SELECT parent.parent_run_id AS id
	      UNION
      SELECT NULLIF(source.value->>'parent_run_id', '')::uuid FROM jsonb_array_elements(parent.provenance) AS source(value)
	    ) inherited
	    JOIN bot.runs ancestor ON ancestor.id = inherited.id AND ancestor.conv_id = ?
	)
	SELECT DISTINCT COALESCE(parent.message_id, receipt.message_id) AS message_id
	FROM parents JOIN bot.runs parent ON parent.id = parents.id
	LEFT JOIN messaging.bot_output_submissions receipt ON receipt.bot_id = parent.bot_id
	  AND receipt.binding_id = parent.binding_id AND receipt.conv_id = parent.conv_id
	  AND receipt.output_key = parent.id::text || ':final'
	WHERE parent.status IN ('succeeded', 'failed', 'stopped')
	  AND COALESCE(parent.message_id, receipt.message_id) IS NOT NULL`, convID, parentIDs, convID).Scan(&ids).Error
	return ids, err
}
