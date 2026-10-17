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

// KnowledgeSource identifies the exact accepted catalog metadata or document/chunk
// generation. It is provenance, not a grant: callers separately check access.
// Chunk evidence without an accepted generation fails closed; a catalog version
// cannot stand in for a document or be mixed with chunk evidence.
type KnowledgeSource struct {
	KnowledgeID           string `json:"knowledge_id,omitempty"`
	KnowledgeDocID        string `json:"knowledge_doc_id,omitempty"`
	KnowledgeChunkID      string `json:"knowledge_chunk_id,omitempty"`
	KnowledgeContentHash  string `json:"knowledge_content_hash,omitempty"`
	KnowledgeDocumentHash string `json:"knowledge_document_hash,omitempty"`
	KnowledgeMetadataHash string `json:"knowledge_metadata_hash,omitempty"`
}

// KnowledgeMetadata contains only the catalog content consumed by a Run, never
// provider credentials, embedding models or retrieval configuration.
type KnowledgeMetadata struct {
	ID          string `gorm:"column:id" json:"kb_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

func (metadata KnowledgeMetadata) SourceVersion() KnowledgeSource {
	return KnowledgeSource{KnowledgeID: metadata.ID, KnowledgeMetadataHash: knowledgeFingerprint(metadata)}
}

func CurrentKnowledgeMetadata(tx *gorm.DB, id string, lock bool) (*KnowledgeMetadata, error) {
	if identity.Validate(id) != nil {
		return nil, status.Error(codes.PermissionDenied, "knowledge metadata unavailable")
	}
	query := tx.Table("knowledge.knowledge_bases").Select("id,name,description,mode").Where("id = ? AND status = 'active' AND mode = 'rag'", id)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	var metadata KnowledgeMetadata
	if err := query.Take(&metadata).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.PermissionDenied, "knowledge metadata unavailable")
		}
		return nil, err
	}
	return &metadata, nil
}

// KnowledgeDocumentMetadata is the non-sensitive document catalog consumed by a
// Run. A pending or failed document may be listed without granting body access.
type KnowledgeDocumentMetadata struct {
	ID         string `json:"doc_id"`
	KBID       string `json:"kb_id"`
	Title      string `json:"title"`
	FileType   string `json:"file_type"`
	Status     string `json:"status"`
	ChunkCount int    `json:"chunk_count"`
}

func (document KnowledgeDocumentMetadata) SourceVersion() KnowledgeSource {
	return KnowledgeSource{KnowledgeID: document.KBID, KnowledgeDocID: document.ID, KnowledgeDocumentHash: knowledgeFingerprint(document)}
}

func CurrentKnowledgeDocumentMetadata(tx *gorm.DB, id string, lock bool) (*KnowledgeDocumentMetadata, error) {
	if identity.Validate(id) != nil {
		return nil, status.Error(codes.PermissionDenied, "knowledge document metadata unavailable")
	}
	query := tx.Table("knowledge.documents d").
		Select("d.id,d.kb_id,d.title,d.file_type,d.status,d.chunk_count").
		Joins("JOIN knowledge.knowledge_bases k ON k.id = d.kb_id").
		Where("d.id = ? AND k.status = 'active' AND k.mode = 'rag'", id)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "d, k", Raw: true}})
	}
	var document KnowledgeDocumentMetadata
	if err := query.Take(&document).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.PermissionDenied, "knowledge document metadata unavailable")
		}
		return nil, err
	}
	return &document, nil
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
	if version.KnowledgeMetadataHash != "" {
		if version.KnowledgeDocID != "" || version.KnowledgeChunkID != "" || version.KnowledgeContentHash != "" || version.KnowledgeDocumentHash != "" {
			return status.Error(codes.PermissionDenied, "mixed knowledge source kinds")
		}
		metadata, err := CurrentKnowledgeMetadata(tx, version.KnowledgeID, lock)
		if err != nil {
			return err
		}
		if metadata.SourceVersion() != version {
			return status.Error(codes.PermissionDenied, "knowledge metadata replaced")
		}
		return nil
	}
	if version.KnowledgeDocID != "" && version.KnowledgeChunkID == "" && version.KnowledgeContentHash == "" && version.KnowledgeDocumentHash != "" {
		document, err := CurrentKnowledgeDocumentMetadata(tx, version.KnowledgeDocID, lock)
		if err != nil {
			return err
		}
		if document.SourceVersion() != version {
			return status.Error(codes.PermissionDenied, "knowledge document metadata replaced")
		}
		return nil
	}
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
