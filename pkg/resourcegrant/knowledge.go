package resourcegrant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeSource identifies the exact accepted document/chunk generation.
// A source is evidence, not an authorization grant; callers also check access.
// Knowledge evidence without the accepted chunk generation fails closed; current
// metadata cannot be substituted for the version that was actually consumed.
type KnowledgeSource struct {
	KnowledgeID           string `json:"knowledge_id,omitempty"`
	KnowledgeDocID        string `json:"knowledge_doc_id,omitempty"`
	KnowledgeChunkID      string `json:"knowledge_chunk_id,omitempty"`
	KnowledgeContentHash  string `json:"knowledge_content_hash,omitempty"`
	KnowledgeDocumentHash string `json:"knowledge_document_hash,omitempty"`
}

// KnowledgeChunk verifies returned evidence against the existing knowledge store.
// It is not a retrieval API and does not bypass resource or sharing authorization.
type KnowledgeChunk struct {
	ID                  string
	DocID               string
	KBID                string
	ParentChunkID       *string
	ChunkIndex          int
	Content             string
	Metadata            json.RawMessage
	DocTitle            string
	KBName              string
	DocumentContentHash string
	DocumentMetadata    json.RawMessage
}

func CurrentKnowledgeChunk(tx *gorm.DB, id string, lock bool) (*KnowledgeChunk, error) {
	if identity.Validate(id) != nil {
		return nil, status.Error(codes.PermissionDenied, "knowledge source unavailable")
	}
	var chunk KnowledgeChunk
	query := tx.Table("knowledge.document_chunks c").
		Select("c.id, c.doc_id, c.kb_id, c.parent_chunk_id, c.chunk_index, c.content, c.metadata, d.title AS doc_title, k.name AS kb_name, d.content_hash AS document_content_hash, d.metadata AS document_metadata").
		Joins("JOIN knowledge.documents d ON d.id = c.doc_id AND d.kb_id = c.kb_id").
		Joins("JOIN knowledge.knowledge_bases k ON k.id = d.kb_id").
		Where("c.id = ? AND d.status = 'ready' AND k.status = 'active'", id)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "c, d, k", Raw: true}})
	}
	if err := query.Take(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.PermissionDenied, "knowledge source unavailable")
		}
		return nil, err
	}
	return &chunk, nil
}

func knowledgeFingerprint(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (chunk *KnowledgeChunk) SourceVersion() KnowledgeSource {
	return KnowledgeSource{KnowledgeID: chunk.KBID, KnowledgeDocID: chunk.DocID, KnowledgeChunkID: chunk.ID,
		KnowledgeContentHash: knowledgeFingerprint(struct {
			Content  string
			Index    int
			Parent   *string
			Metadata json.RawMessage
		}{chunk.Content, chunk.ChunkIndex, chunk.ParentChunkID, chunk.Metadata}),
		KnowledgeDocumentHash: knowledgeFingerprint(struct {
			Hash, Title string
			Metadata    json.RawMessage
		}{chunk.DocumentContentHash, chunk.DocTitle, chunk.DocumentMetadata})}
}

func CheckKnowledgeSource(tx *gorm.DB, version KnowledgeSource, lock bool) error {
	if version.KnowledgeID == "" && version.KnowledgeDocID == "" && version.KnowledgeChunkID == "" {
		return nil
	}
	chunk, err := CurrentKnowledgeChunk(tx, version.KnowledgeChunkID, lock)
	if err != nil {
		return err
	}
	if chunk.SourceVersion() != version {
		return status.Error(codes.PermissionDenied, "knowledge source replaced")
	}
	return nil
}
