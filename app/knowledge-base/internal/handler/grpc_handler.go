package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/parser"
	"github.com/maomeng/aim/app/knowledge-base/internal/metrics"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/knowledgecontract"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

type KnowledgeBaseHandler struct {
	pb.UnimplementedKnowledgeBaseServer
	DB           *gorm.DB
	KBRepo       domain.KBRepo
	DocRepo      domain.DocumentRepo
	FileStore    domain.FileStore
	VectorStore  domain.VectorStore
	Producer     *kafka.Producer
	RetrievePipe *pipeline.RetrievePipeline
	Logger       logx.Logger
}

func (h *KnowledgeBaseHandler) CreateKB(ctx context.Context, req *pb.CreateKBReq) (*pb.KBRsp, error) {
	callerID := getCallerID(ctx)
	if callerID == "" {
		return nil, errors.ErrUnauthorized
	}
	ownerType := req.OwnerType
	if ownerType == "" {
		ownerType = "user"
	}
	ownerID := req.OwnerId
	switch ownerType {
	case "platform":
		if !isAdmin(ctx) {
			return nil, domain.ErrForbidden
		}
		if ownerID != nil {
			return nil, errors.ErrInvalidParam
		}
	case "user":
		if ownerID == nil {
			ownerID = &callerID
		}
		if err := identity.Validate(*ownerID); err != nil {
			return nil, errors.Wrap(errors.CodeInvalidParam, "invalid owner_id", err)
		}
		if *ownerID != callerID && !isAdmin(ctx) {
			return nil, domain.ErrForbidden
		}
	default:
		return nil, errors.ErrInvalidParam
	}
	if err := validateModelReference(req.EmbeddingModelId, false, false); err != nil {
		return nil, err
	}
	cfg, err := convertPipelineConfig(req.PipelineConfig)
	if err != nil {
		return nil, err
	}
	kbID, err := identity.New()
	if err != nil {
		return nil, fmt.Errorf("generate kb id failed: %w", err)
	}
	kb := &domain.KnowledgeBase{
		ID:               kbID,
		OwnerType:        ownerType,
		OwnerID:          ownerID,
		Name:             req.Name,
		Description:      req.Description,
		Mode:             req.Mode,
		EmbeddingModel:   req.EmbeddingModel,
		EmbeddingModelID: req.EmbeddingModelId,
		PipelineConfig:   cfg,
		Status:           "active",
	}
	if err := h.KBRepo.Create(ctx, kb); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "create kb failed", err)
	}
	h.Logger.WithContext(ctx).Infof("knowledge base created: kb_id=%s name=%s owner_type=%s caller_id=%s", kb.ID, kb.Name, kb.OwnerType, callerID)
	return toKBRsp(kb), nil
}

func (h *KnowledgeBaseHandler) UpdateKB(ctx context.Context, req *pb.UpdateKBReq) (*pb.KBRsp, error) {
	kb, err := h.loadKnowledgeBase(ctx, req.KbId, true)
	if err != nil {
		return nil, err
	}
	if kb.Status == "deleting" {
		return nil, domain.ErrKBNotFound
	}
	if req.GetName() != "" {
		kb.Name = req.GetName()
	}
	if req.GetDescription() != "" {
		kb.Description = req.GetDescription()
	}
	if err := validateModelReference(req.EmbeddingModelId, req.ClearEmbeddingModelId, true); err != nil {
		return nil, err
	}
	if req.ClearEmbeddingModelId {
		kb.EmbeddingModelID = nil
		kb.EmbeddingModel = ""
	} else {
		if req.GetEmbeddingModel() != "" {
			kb.EmbeddingModel = req.GetEmbeddingModel()
		}
		if req.EmbeddingModelId != nil {
			kb.EmbeddingModelID = req.EmbeddingModelId
			if req.GetEmbeddingModel() == "" {
				kb.EmbeddingModel = ""
			}
		}
	}
	if req.GetPipelineConfig() != nil {
		cfg, err := mergePipelineConfig(req.GetPipelineConfig(), kb.PipelineConfig, true)
		if err != nil {
			return nil, err
		}
		kb.PipelineConfig = cfg
	}
	if req.GetStatus() != "" {
		kb.Status = req.GetStatus()
	}
	if err := h.KBRepo.Update(ctx, kb); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update kb failed", err)
	}
	return toKBRsp(kb), nil
}

func (h *KnowledgeBaseHandler) DeleteKB(ctx context.Context, req *pb.DeleteKBReq) (*emptypb.Empty, error) {
	kb, err := h.loadKnowledgeBase(ctx, req.KbId, true)
	if err != nil {
		return nil, err
	}
	// Quiesce uploads/retries before enumerating documents. Repository creates
	// serialize with this update on the KB row; ingestion checks the same state.
	kb.Status = "deleting"
	if err := h.KBRepo.Update(ctx, kb); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "mark kb for deletion failed", err)
	}
	bindings, err := h.KBRepo.ListBindingsByKB(ctx, kb.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list kb bindings failed", err)
	}
	for _, binding := range bindings {
		if err := h.KBRepo.Unbind(ctx, kb.ID, binding.TargetType, binding.TargetID); err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "remove kb binding failed", err)
		}
	}
	for {
		// Removing a page shifts the remaining documents to offset zero.
		docs, _, err := h.DocRepo.ListByKB(ctx, kb.ID, 0, 100, "", "")
		if err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "list kb documents failed", err)
		}
		if len(docs) == 0 {
			break
		}
		for _, doc := range docs {
			if err := h.DocRepo.WithDocumentLock(ctx, doc.ID, func(lockCtx context.Context) error {
				current, err := h.DocRepo.Get(lockCtx, doc.ID)
				if stderrors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				if err != nil {
					return errors.Wrap(errors.CodeDBError, "get document for deletion failed", err)
				}
				if current.KBID != kb.ID {
					return domain.ErrForbidden
				}
				return h.deleteDocumentData(lockCtx, current)
			}); err != nil {
				return nil, err
			}
		}
	}
	if err := h.KBRepo.Delete(ctx, kb.ID); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "delete kb failed", err)
	}
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) GetKB(ctx context.Context, req *pb.GetKBReq) (*pb.KBRsp, error) {
	kb, err := h.loadKnowledgeBase(ctx, req.KbId, false)
	if err != nil {
		return nil, err
	}
	return toKBRsp(kb), nil
}

func (h *KnowledgeBaseHandler) ListKBs(ctx context.Context, req *pb.ListKBsReq) (*pb.ListKBsRsp, error) {
	offset, limit := int(req.Offset), int(req.Limit)
	if offset < 0 || limit < 0 || limit > knowledgecontract.MaxListLimit {
		return nil, errors.New(errors.CodeInvalidParam, "invalid knowledge list pagination")
	}
	if limit == 0 {
		limit = knowledgecontract.DefaultListLimit
	}
	if _, run := resourcegrant.Incoming(ctx); run {
		kbs, err := h.runKnowledgeBases(ctx)
		if err != nil {
			return nil, err
		}
		start := min(offset, len(kbs))
		end := start + min(limit, len(kbs)-start)
		items := make([]*pb.KBRsp, 0, end-start)
		for _, kb := range kbs[start:end] {
			// Discovery is not a management configuration API. In particular no
			// parsing/VLM provider credentials or model offerings reach the Run.
			items = append(items, &pb.KBRsp{Id: kb.ID, Name: kb.Name, Description: kb.Description, Mode: kb.Mode, Status: kb.Status})
		}
		if _, err := h.checkRunKnowledgeAccess(ctx); err != nil {
			return nil, err
		}
		return &pb.ListKBsRsp{Items: items, Total: int64(len(kbs))}, nil
	}
	ownerID := getCallerID(ctx)
	if ownerID == "" {
		return nil, errors.ErrUnauthorized
	}
	kbs, total, err := h.KBRepo.ListByOwner(ctx, ownerID, offset, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list kbs failed", err)
	}
	items := make([]*pb.KBRsp, len(kbs))
	for i, kb := range kbs {
		items[i] = toKBRsp(&kb)
	}
	h.Logger.WithContext(ctx).Infof("knowledge bases listed: user_id=%s count=%d", ownerID, total)
	return &pb.ListKBsRsp{Items: items, Total: total}, nil
}

func (h *KnowledgeBaseHandler) UploadDocument(stream pb.KnowledgeBase_UploadDocumentServer) error {
	ctx := stream.Context()
	var meta *pb.UploadDocumentReq_MetaChunk
	var fileBytes bytes.Buffer
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch d := req.Data.(type) {
		case *pb.UploadDocumentReq_Meta:
			meta = d.Meta
		case *pb.UploadDocumentReq_Content:
			fileBytes.Write(d.Content)
		}
	}
	if meta == nil {
		return errors.ErrInvalidParam
	}
	fileType := meta.FileType
	if fileType == "" {
		fileType = "txt"
	}
	if !parser.IsFileTypeSupported(fileType) {
		return domain.ErrInvalidFileType
	}
	kbID := getKBIDFromContext(ctx)
	if kbID == "" {
		return errors.ErrInvalidParam
	}
	kb, err := h.loadKnowledgeBase(ctx, kbID, true)
	if err != nil {
		return err
	}
	if kb.Status != "active" {
		return domain.ErrKBNotFound
	}
	contentHash := fmt.Sprintf("%x", sha256.Sum256(fileBytes.Bytes()))

	// Check for duplicate content within the same KB
	if existing, err := h.DocRepo.GetByHash(ctx, kbID, contentHash); err == nil && existing != nil {
		return errors.New(errors.CodeConflict, "file already exists in this knowledge base")
	} else if err != nil && !stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.Wrap(errors.CodeDBError, "check document content failed", err)
	}
	var pipelineOverride *domain.PipelineConfig
	if meta.PipelineOverride != nil {
		cfg, err := convertPipelineConfig(meta.PipelineOverride)
		if err != nil {
			return err
		}
		pipelineOverride = &cfg
	}
	docID, err := identity.New()
	if err != nil {
		return fmt.Errorf("generate doc id failed: %w", err)
	}
	minioKey := fmt.Sprintf("knowledge/%s/%s/original", kbID, docID)
	doc := &domain.Document{
		ID:               docID,
		KBID:             kbID,
		Title:            meta.Title,
		FileType:         fileType,
		FileSize:         meta.FileSize,
		OriginalFilename: meta.OriginalFilename,
		MinioKey:         minioKey,
		ContentHash:      contentHash,
		Status:           domain.DocStatusPending,
		PipelineOverride: pipelineOverride,
		Metadata:         parseJSONMeta(meta.Metadata),
	}
	// Storage-owned object references cannot be supplied through user metadata.
	delete(doc.Metadata, "image_keys")
	err = h.DocRepo.WithDocumentLock(ctx, doc.ID, func(lockCtx context.Context) error {
		if err := h.FileStore.Put(lockCtx, minioKey, bytes.NewReader(fileBytes.Bytes()), meta.FileSize, contentType(fileType)); err != nil {
			return errors.Wrap(errors.CodeIOError, "upload file failed", err)
		}
		if err := h.DocRepo.Create(lockCtx, doc); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(lockCtx), 5*time.Second)
			defer cancel()
			if cleanupErr := h.FileStore.Delete(cleanupCtx, minioKey); cleanupErr != nil {
				err = stderrors.Join(err, fmt.Errorf("remove uploaded object: %w", cleanupErr))
			}
			return errors.Wrap(errors.CodeDBError, "create document failed", err)
		}
		if err := h.KBRepo.UpdateCounts(lockCtx, kbID); err != nil {
			h.Logger.WithContext(lockCtx).Errorf("failed to update kb counts after upload: %v", err)
		}
		eventData, err := json.Marshal(DocumentUploadedEvent{DocID: doc.ID})
		if err != nil {
			return errors.Wrap(errors.CodeInternal, "marshal upload event failed", err)
		}
		if err := h.Producer.Send(lockCtx, doc.ID, eventData); err != nil {
			statusCtx, cancel := context.WithTimeout(context.WithoutCancel(lockCtx), 5*time.Second)
			defer cancel()
			if statusErr := h.DocRepo.UpdateStatus(statusCtx, doc.ID, domain.DocStatusFailed, "publish upload event failed: "+err.Error()); statusErr != nil {
				err = stderrors.Join(err, statusErr)
			}
			return errors.Wrap(errors.CodeMQError, "publish upload event failed", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.Logger.WithContext(ctx).Infof("document uploaded: doc_id=%s kb_id=%s file_name=%s", doc.ID, kbID, meta.OriginalFilename)
	return stream.SendAndClose(toDocumentRsp(doc))
}

func (h *KnowledgeBaseHandler) GetDocument(ctx context.Context, req *pb.GetDocumentReq) (*pb.DocumentRsp, error) {
	doc, err := h.loadDocument(ctx, req.DocId, false)
	if err != nil {
		return nil, err
	}
	return toDocumentRsp(doc), nil
}

func (h *KnowledgeBaseHandler) ListDocuments(ctx context.Context, req *pb.ListDocumentsReq) (*pb.ListDocumentsRsp, error) {
	kb, err := h.loadKnowledgeBase(ctx, req.KbId, false)
	if err != nil {
		return nil, err
	}
	if _, bot := resourcegrant.Incoming(ctx); bot && kb.Status != "active" {
		return nil, domain.ErrDocumentUnavailable
	}
	offset, limit, err := readingPage(req.Offset, req.Limit, knowledgecontract.DefaultDocumentListLimit, knowledgecontract.MaxDocumentListLimit)
	if err != nil {
		return nil, err
	}
	docs, total, err := h.DocRepo.ListByKB(ctx, req.KbId, offset, limit, req.Status, req.Title)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list documents failed", err)
	}
	resp := &pb.ListDocumentsRsp{Items: make([]*pb.DocumentRsp, 0, len(docs)), Total: total}
	used := readingPageEnvelopeBytes
	for i := range docs {
		item := documentReadingInfo(ctx, &docs[i])
		fits, err := readingRecordFits(item, &used)
		if err != nil {
			return nil, err
		}
		if !fits {
			if len(resp.Items) == 0 {
				return nil, domain.ErrReadingTooLarge
			}
			break
		}
		resp.Items = append(resp.Items, item)
	}
	resp.NextOffset = req.Offset + int32(len(resp.Items))
	resp.HasMore = len(resp.Items) > 0 && int64(resp.NextOffset) < total
	resp.Truncated = resp.HasMore
	if _, err := h.loadKnowledgeBase(ctx, req.KbId, false); err != nil {
		return nil, err
	}
	return resp, nil
}

func (h *KnowledgeBaseHandler) GetDocumentContent(ctx context.Context, req *pb.GetDocumentContentReq) (*pb.GetDocumentContentResp, error) {
	doc, err := h.loadDocument(ctx, req.DocId, false)
	if err != nil {
		return nil, err
	}
	if doc.MinioKey == "" {
		return &pb.GetDocumentContentResp{
			MimeType: contentType(doc.FileType),
			FileSize: doc.FileSize,
		}, nil
	}
	reader, err := h.FileStore.Get(ctx, doc.MinioKey)
	if err != nil {
		return nil, errors.Wrap(errors.CodeIOError, "failed to read file from storage", err)
	}
	defer func() {
		// Closing a read-only storage stream does not change the read result.
		_ = reader.Close()
	}()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, errors.Wrap(errors.CodeIOError, "failed to read file content", err)
	}
	return &pb.GetDocumentContentResp{
		Content:  buf.String(),
		MimeType: contentType(doc.FileType),
		FileSize: doc.FileSize,
	}, nil
}

func (h *KnowledgeBaseHandler) ListChunks(ctx context.Context, req *pb.ListChunksReq) (*pb.ListChunksResp, error) {
	if _, err := h.loadDocument(ctx, req.DocId, false); err != nil {
		return nil, err
	}
	offset, limit, err := readingPage(req.Offset, req.Limit, knowledgecontract.DefaultChunkReadLimit, knowledgecontract.MaxChunkReadLimit)
	if err != nil {
		return nil, err
	}
	if (req.StartIndex != nil && *req.StartIndex < 0) || (req.EndIndex != nil && *req.EndIndex < 0) ||
		(req.StartIndex != nil && req.EndIndex != nil && *req.StartIndex > *req.EndIndex) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid inclusive chunk index range")
	}
	var resp *pb.ListChunksResp
	err = h.DocRepo.WithDocumentLock(ctx, req.DocId, func(ctx context.Context) error {
		doc, err := h.loadDocument(ctx, req.DocId, false)
		if err != nil {
			return err
		}
		switch doc.Status {
		case domain.DocStatusReady:
		case domain.DocStatusPending, domain.DocStatusParsing, domain.DocStatusChunking, domain.DocStatusEmbedding:
			return domain.ErrDocumentNotReady
		default:
			return domain.ErrDocumentUnavailable
		}
		kb, err := h.loadKnowledgeBase(ctx, doc.KBID, false)
		if err != nil {
			return err
		}
		if kb.Status != "active" {
			return domain.ErrDocumentUnavailable
		}
		total, err := h.DocRepo.CountChunksByDocID(ctx, doc.ID, req.StartIndex, req.EndIndex)
		if err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to count chunks", err)
		}
		if total == 0 {
			readable := total
			if req.StartIndex != nil || req.EndIndex != nil {
				readable, err = h.DocRepo.CountChunksByDocID(ctx, doc.ID, nil, nil)
				if err != nil {
					return errors.Wrap(errors.CodeDBError, "failed to count readable chunks", err)
				}
			}
			if readable == 0 {
				return domain.ErrNoReadableContent
			}
		}
		chunks, err := h.DocRepo.GetChunksByDocID(ctx, doc.ID, offset, limit, req.StartIndex, req.EndIndex)
		if err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to list chunks", err)
		}
		resp = &pb.ListChunksResp{Chunks: make([]*pb.ChunkInfo, 0, len(chunks)), Total: total}
		used := readingPageEnvelopeBytes
		for _, ch := range chunks {
			metadata, err := sourceMetadataJSON(ch.Metadata)
			if err != nil {
				return err
			}
			item := &pb.ChunkInfo{
				Id: ch.ID, DocId: ch.DocID, ChunkIndex: int32(ch.ChunkIndex),
				Content: ch.Content, TokenCount: int32(ch.TokenCount),
				Metadata: metadata, CreatedAt: ch.CreatedAt.Unix(),
			}
			fits, err := readingRecordFits(item, &used)
			if err != nil {
				return err
			}
			if !fits {
				if len(resp.Chunks) == 0 {
					return domain.ErrReadingTooLarge
				}
				break
			}
			resp.Chunks = append(resp.Chunks, item)
		}
		resp.NextOffset = req.Offset + int32(len(resp.Chunks))
		resp.HasMore = len(resp.Chunks) > 0 && int64(resp.NextOffset) < total
		resp.Truncated = resp.HasMore
		if len(resp.Chunks) > 0 {
			start, end := resp.Chunks[0].ChunkIndex, resp.Chunks[len(resp.Chunks)-1].ChunkIndex
			resp.ActualStartIndex, resp.ActualEndIndex = &start, &end
		}
		_, err = h.loadDocument(ctx, req.DocId, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// This covers both page envelopes, scalar fields, and JSON array separators.
const readingPageEnvelopeBytes = 512

func readingPage(offset, limit int32, defaultLimit, maxLimit int) (int, int, error) {
	if offset < 0 || limit < 0 || int(limit) > maxLimit {
		return 0, 0, errors.New(errors.CodeInvalidParam, "invalid reading pagination")
	}
	if limit == 0 {
		limit = int32(defaultLimit)
	}
	if int64(offset)+int64(limit) > 1<<31-1 {
		return 0, 0, errors.New(errors.CodeInvalidParam, "reading offset exceeds supported range")
	}
	return int(offset), int(limit), nil
}

func readingRecordFits(record proto.Message, used *int) (bool, error) {
	raw, err := protocol.Marshal(record)
	if err != nil {
		return false, errors.Wrap(errors.CodeInternal, "encode reading record failed", err)
	}
	size := len(raw) + 1
	if size > knowledgecontract.MaxReadingResponseBytes-*used {
		return false, nil
	}
	*used += size
	return true, nil
}

func documentReadingInfo(ctx context.Context, doc *domain.Document) *pb.DocumentRsp {
	if _, bot := resourcegrant.Incoming(ctx); !bot {
		return toDocumentRsp(doc)
	}
	// Execution discovery is not a management view: do not expose parser errors,
	// user metadata, filenames, or storage-owned references to the model.
	return &pb.DocumentRsp{
		Id: doc.ID, KbId: doc.KBID, Title: doc.Title, FileType: doc.FileType,
		Status: string(doc.Status), ChunkCount: int32(doc.ChunkCount),
	}
}

func (h *KnowledgeBaseHandler) DeleteDocument(ctx context.Context, req *pb.DeleteDocumentReq) (*emptypb.Empty, error) {
	if _, err := h.loadDocument(ctx, req.DocId, true); err != nil {
		return nil, err
	}
	err := h.DocRepo.WithDocumentLock(ctx, req.DocId, func(lockCtx context.Context) error {
		doc, err := h.loadDocument(lockCtx, req.DocId, true)
		if err != nil {
			return err
		}
		if doc.Status != domain.DocStatusReady && doc.Status != domain.DocStatusFailed {
			return domain.ErrDocumentProcessing
		}
		if err := h.deleteDocumentData(lockCtx, doc); err != nil {
			return err
		}
		if err := h.KBRepo.UpdateCounts(lockCtx, doc.KBID); err != nil {
			return errors.Wrap(errors.CodeDBError, "update kb counts after delete failed", err)
		}
		h.Logger.WithContext(lockCtx).Infof("document deleted: doc_id=%s kb_id=%s", doc.ID, doc.KBID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) RetryDocument(ctx context.Context, req *pb.RetryDocumentReq) (*pb.DocumentRsp, error) {
	if _, err := h.loadDocument(ctx, req.DocId, true); err != nil {
		return nil, err
	}
	var doc *domain.Document
	err := h.DocRepo.WithDocumentLock(ctx, req.DocId, func(lockCtx context.Context) error {
		var err error
		doc, err = h.loadDocument(lockCtx, req.DocId, true)
		if err != nil {
			return err
		}
		kb, err := h.loadKnowledgeBase(lockCtx, doc.KBID, true)
		if err != nil {
			return err
		}
		if kb.Status != "active" {
			return domain.ErrKBNotFound
		}
		if doc.Status != domain.DocStatusFailed {
			return domain.ErrDocumentNotFailed
		}
		doc.Status = domain.DocStatusPending
		doc.ErrorMessage = ""
		if err := h.DocRepo.Update(lockCtx, doc); err != nil {
			return errors.Wrap(errors.CodeDBError, "reset document status failed", err)
		}
		eventData, err := json.Marshal(DocumentUploadedEvent{DocID: doc.ID})
		if err != nil {
			return errors.Wrap(errors.CodeInternal, "marshal retry event failed", err)
		}
		if err := h.Producer.Send(lockCtx, doc.ID, eventData); err != nil {
			statusCtx, cancel := context.WithTimeout(context.WithoutCancel(lockCtx), 5*time.Second)
			defer cancel()
			if statusErr := h.DocRepo.UpdateStatus(statusCtx, doc.ID, domain.DocStatusFailed, "publish retry event failed: "+err.Error()); statusErr != nil {
				err = stderrors.Join(err, statusErr)
			}
			return errors.Wrap(errors.CodeMQError, "publish retry event failed", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toDocumentRsp(doc), nil
}

func (h *KnowledgeBaseHandler) Retrieve(ctx context.Context, req *pb.RetrieveReq) (*pb.RetrieveRsp, error) {
	limit := knowledgecontract.DefaultSearchLimit
	if req.Limit != nil {
		limit = int(*req.Limit)
		if limit < 1 || limit > knowledgecontract.MaxSearchLimit {
			return nil, errors.New(errors.CodeInvalidParam, "knowledge search limit must be between 1 and 50")
		}
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New(errors.CodeInvalidParam, "knowledge search query is required")
	}
	kbs, docIDs, err := h.resolveSearchScope(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(kbs) == 0 {
		return &pb.RetrieveRsp{Limit: int32(limit)}, nil
	}
	cfg, embeddingModelID, ownerID, err := h.resolveSearchConfiguration(ctx, kbs, limit)
	if err != nil {
		return nil, err
	}
	if h.RetrievePipe == nil || h.RetrievePipe.Embedder == nil || h.RetrievePipe.VectorStore == nil {
		return nil, errors.New(errors.CodeServiceDown, "knowledge retrieval unavailable")
	}
	kbIDs := make([]string, len(kbs))
	names := make(map[string]string, len(kbs))
	for i, kb := range kbs {
		kbIDs[i], names[kb.ID] = kb.ID, kb.Name
	}
	metrics.KbSearchTotal.Inc("rag")
	pipe := *h.RetrievePipe
	pipe.Authorize = func(ctx context.Context) error {
		return h.authorizeSearchScope(ctx, req, kbs)
	}
	items, err := pipe.Retrieve(ctx, kbIDs, docIDs, req.Query, limit, cfg, embeddingModelID, ownerID)
	if err != nil {
		return nil, err
	}
	pbItems := make([]*pb.RetrieveItem, len(items))
	for i, item := range items {
		matchedMeta, err := sourceMetadataJSON(item.Metadata)
		if err != nil {
			return nil, err
		}
		contextMeta, err := sourceMetadataJSON(item.ContextMetadata)
		if err != nil {
			return nil, err
		}
		pbItems[i] = &pb.RetrieveItem{
			ChunkId: item.ChunkID, Content: item.Content, Score: item.Score,
			DocId: item.DocID, DocTitle: item.DocTitle, KbId: item.KBID,
			KbName: names[item.KBID], MatchedContent: item.MatchedContent,
			ChunkIndex: int32(item.ChunkIndex), MetadataJson: matchedMeta,
			ContextChunkId: item.ContextChunkID, ContextChunkIndex: item.ContextChunkIndex,
			ContextMetadataJson: contextMeta,
		}
	}
	h.Logger.WithContext(ctx).Infof("retrieve: kb_count=%d query_len=%d result_count=%d", len(kbIDs), len(req.Query), len(pbItems))
	return &pb.RetrieveRsp{Items: pbItems, Limit: int32(limit)}, nil
}

func (h *KnowledgeBaseHandler) Bind(ctx context.Context, req *pb.BindReq) (*emptypb.Empty, error) {
	kb, err := h.loadKnowledgeBase(ctx, req.KbId, true)
	if err != nil {
		return nil, err
	}
	if kb.Status != "active" {
		return nil, domain.ErrKBNotFound
	}
	if err := validateBindingTarget(req.TargetType, req.TargetId); err != nil {
		return nil, err
	}
	bindingID, err := identity.New()
	if err != nil {
		return nil, fmt.Errorf("generate binding id failed: %w", err)
	}
	binding := &domain.KnowledgeBinding{
		ID:         bindingID,
		KBID:       req.KbId,
		TargetType: req.TargetType,
		TargetID:   req.TargetId,
	}
	if err := h.KBRepo.Bind(ctx, binding); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "bind failed", err)
	}
	h.Logger.WithContext(ctx).Infof("kb bound: kb_id=%s target_type=%s target_id=%s", req.KbId, req.TargetType, req.TargetId)
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) Unbind(ctx context.Context, req *pb.UnbindReq) (*emptypb.Empty, error) {
	if _, err := h.loadKnowledgeBase(ctx, req.KbId, true); err != nil {
		return nil, err
	}
	if err := validateBindingTarget(req.TargetType, req.TargetId); err != nil {
		return nil, err
	}
	if err := h.KBRepo.Unbind(ctx, req.KbId, req.TargetType, req.TargetId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "unbind failed", err)
	}
	h.Logger.WithContext(ctx).Infof("kb unbound: kb_id=%s target_type=%s target_id=%s", req.KbId, req.TargetType, req.TargetId)
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) ListBindings(ctx context.Context, req *pb.ListBindingsReq) (*pb.ListBindingsRsp, error) {
	if getCallerID(ctx) == "" {
		return nil, errors.ErrUnauthorized
	}
	if err := validateBindingTarget(req.TargetType, req.TargetId); err != nil {
		return nil, err
	}
	bindings, err := h.KBRepo.ListBindingsByTarget(ctx, req.TargetType, req.TargetId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list bindings failed", err)
	}
	items := make([]*pb.BindingItem, 0, len(bindings))
	for _, b := range bindings {
		kb, err := h.loadKnowledgeBase(ctx, b.KBID, false)
		if err != nil {
			if stderrors.Is(err, domain.ErrForbidden) {
				continue
			}
			return nil, err
		}
		items = append(items, &pb.BindingItem{
			Id:         b.ID,
			KbId:       b.KBID,
			KbName:     b.KBName,
			Mode:       kb.Mode,
			TargetType: b.TargetType,
			TargetId:   b.TargetID,
			CreatedAt:  b.CreatedAt.Unix(),
		})
	}
	return &pb.ListBindingsRsp{Items: items}, nil
}

func (h *KnowledgeBaseHandler) ListBoundTargets(ctx context.Context, req *pb.ListBoundTargetsReq) (*pb.ListBoundTargetsRsp, error) {
	if _, err := h.loadKnowledgeBase(ctx, req.KbId, false); err != nil {
		return nil, err
	}
	bindings, err := h.KBRepo.ListBindingsByKB(ctx, req.KbId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list bound targets failed", err)
	}
	items := make([]*pb.BoundTargetItem, len(bindings))
	for i, b := range bindings {
		items[i] = &pb.BoundTargetItem{
			TargetType: b.TargetType,
			TargetId:   b.TargetID,
			CreatedAt:  b.CreatedAt.Unix(),
		}
	}
	return &pb.ListBoundTargetsRsp{Items: items}, nil
}

func getCallerID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range []string{"x-user-id", "user-id"} {
			if vals := md.Get(key); len(vals) > 0 {
				if identity.Validate(vals[0]) == nil {
					return vals[0]
				}
				return ""
			}
		}
	}
	return ""
}

func ownsKnowledgeBase(kb *domain.KnowledgeBase, callerID string) bool {
	return callerID != "" && kb.OwnerType == "user" && kb.OwnerID != nil && *kb.OwnerID == callerID
}

func (h *KnowledgeBaseHandler) loadKnowledgeBase(ctx context.Context, id string, write bool) (*domain.KnowledgeBase, error) {
	if err := identity.Validate(id); err != nil {
		return nil, errors.Wrap(errors.CodeInvalidParam, "invalid kb_id", err)
	}
	access, bot := resourcegrant.Incoming(ctx)
	callerID := getCallerID(ctx)
	if !bot && callerID == "" {
		return nil, errors.ErrUnauthorized
	}
	kb, err := h.KBRepo.Get(ctx, id)
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrKBNotFound
	}
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get knowledge base failed", err)
	}
	if err := kb.ValidateOwner(); err != nil {
		return nil, domain.ErrForbidden
	}
	if bot {
		if write || h.DB == nil {
			return nil, domain.ErrForbidden
		}
		if _, err := resourcegrant.Check(h.DB.WithContext(ctx), access, "knowledge", id, access.Visibility != "personal", false); err != nil {
			return nil, err
		}
		return kb, nil
	}
	if !ownsKnowledgeBase(kb, callerID) && !isAdmin(ctx) && (write || kb.OwnerType != "platform") {
		return nil, domain.ErrForbidden
	}
	return kb, nil
}

func (h *KnowledgeBaseHandler) loadDocument(ctx context.Context, id string, write bool) (*domain.Document, error) {
	if err := identity.Validate(id); err != nil {
		return nil, errors.Wrap(errors.CodeInvalidParam, "invalid doc_id", err)
	}
	if _, bot := resourcegrant.Incoming(ctx); !bot && getCallerID(ctx) == "" {
		return nil, errors.ErrUnauthorized
	}
	doc, err := h.DocRepo.Get(ctx, id)
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrDocNotFound
	}
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "get document failed", err)
	}
	if _, err := h.loadKnowledgeBase(ctx, doc.KBID, write); err != nil {
		return nil, err
	}
	return doc, nil
}

func validateBindingTarget(targetType, targetID string) error {
	if targetType != "bot" && targetType != "conv" {
		return errors.ErrInvalidParam
	}
	if err := identity.Validate(targetID); err != nil {
		return errors.Wrap(errors.CodeInvalidParam, "invalid target_id", err)
	}
	return nil
}

// The caller holds the document lock. Invalidating readiness first ensures any
// failed external cleanup cannot leave old vector results visible to retrieval.
func (h *KnowledgeBaseHandler) deleteDocumentData(ctx context.Context, doc *domain.Document) error {
	if err := h.DocRepo.UpdateStatus(ctx, doc.ID, domain.DocStatusFailed, "document deletion in progress"); err != nil {
		return errors.Wrap(errors.CodeDBError, "invalidate document before deletion failed", err)
	}
	if err := h.DocRepo.DeleteChunksByDoc(ctx, doc.ID); err != nil {
		return errors.Wrap(errors.CodeDBError, "delete document chunks failed", err)
	}
	if h.VectorStore == nil {
		return errors.New(errors.CodeInternal, "vector store unavailable")
	}
	if err := h.VectorStore.DeleteByDoc(ctx, doc.ID); err != nil {
		return errors.Wrap(errors.CodeIOError, "delete document vectors failed", err)
	}
	prefix := "knowledge/" + doc.KBID + "/" + doc.ID + "/images/"
	deleteImage := func(raw any) error {
		key, ok := raw.(string)
		if !ok || !strings.HasPrefix(key, prefix) || len(key) == len(prefix) || strings.Contains(key[len(prefix):], "/") {
			return errors.New(errors.CodeInvalidParam, "invalid document image object reference")
		}
		if err := h.FileStore.Delete(ctx, key); err != nil {
			return errors.Wrap(errors.CodeIOError, "delete document image failed", err)
		}
		return nil
	}
	switch keys := doc.Metadata["image_keys"].(type) {
	case nil:
	case []string:
		for _, key := range keys {
			if err := deleteImage(key); err != nil {
				return err
			}
		}
	case []any:
		for _, key := range keys {
			if err := deleteImage(key); err != nil {
				return err
			}
		}
	default:
		return errors.New(errors.CodeInvalidParam, "invalid document image object references")
	}
	if doc.MinioKey != "" {
		if err := h.FileStore.Delete(ctx, doc.MinioKey); err != nil {
			return errors.Wrap(errors.CodeIOError, "delete document object failed", err)
		}
	}
	if err := h.DocRepo.Delete(ctx, doc.ID); err != nil {
		return errors.Wrap(errors.CodeDBError, "delete document failed", err)
	}
	return nil
}

func getKBIDFromContext(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("kb-id"); len(vals) > 0 && identity.Validate(vals[0]) == nil {
			return vals[0]
		}
	}
	return ""
}

func isAdmin(ctx context.Context) bool {
	return false
}

func contentType(fileType string) string {
	switch fileType {
	case "pdf":
		return "application/pdf"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "md":
		return "text/markdown"
	case "html":
		return "text/html"
	default:
		return "text/plain"
	}
}

func parseJSONMeta(raw string) map[string]any {
	if raw == "" {
		return make(map[string]any)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return make(map[string]any)
	}
	return m
}

func toKBRsp(kb *domain.KnowledgeBase) *pb.KBRsp {
	rsp := &pb.KBRsp{
		Id:               kb.ID,
		OwnerId:          kb.OwnerID,
		OwnerType:        kb.OwnerType,
		Name:             kb.Name,
		Description:      kb.Description,
		Mode:             kb.Mode,
		EmbeddingModel:   kb.EmbeddingModel,
		EmbeddingModelId: kb.EmbeddingModelID,
		PipelineConfig:   pipelineConfigToProto(kb.PipelineConfig),
		DocCount:         int32(kb.DocCount),
		TotalChunks:      int32(kb.TotalChunks),
		Status:           kb.Status,
		CreatedAt:        kb.CreatedAt.Unix(),
		UpdatedAt:        kb.UpdatedAt.Unix(),
	}
	return rsp
}

func toDocumentRsp(doc *domain.Document) *pb.DocumentRsp {
	rsp := &pb.DocumentRsp{
		Id:               doc.ID,
		KbId:             doc.KBID,
		Title:            doc.Title,
		FileType:         doc.FileType,
		FileSize:         doc.FileSize,
		OriginalFilename: doc.OriginalFilename,
		Status:           string(doc.Status),
		ChunkCount:       int32(doc.ChunkCount),
		ErrorMessage:     doc.ErrorMessage,
		CreatedAt:        doc.CreatedAt.Unix(),
		UpdatedAt:        doc.UpdatedAt.Unix(),
	}
	rsp.Stages = make([]*pb.StageInfo, len(doc.Stages))
	for i, stage := range doc.Stages {
		rsp.Stages[i] = &pb.StageInfo{
			Name: stage.Name, Status: stage.Status, Retries: int32(stage.Retries),
			Error: stage.Error, StartedAt: stage.StartedAt, EndedAt: stage.EndedAt,
		}
	}
	if len(doc.Metadata) > 0 {
		if raw, err := json.Marshal(doc.Metadata); err == nil {
			rsp.Metadata = string(raw)
		}
	}
	return rsp
}

func convertPipelineConfig(pbCfg *pb.PipelineConfig) (domain.PipelineConfig, error) {
	return mergePipelineConfig(pbCfg, domain.PipelineConfig{}, false)
}

func validateModelReference(modelID *string, clear, update bool) error {
	if clear && (!update || modelID != nil) {
		return errors.New(errors.CodeInvalidParam, "clear_model_id requires an update without model_id")
	}
	if modelID != nil {
		if err := identity.Validate(*modelID); err != nil {
			return errors.Wrap(errors.CodeInvalidParam, "invalid model_id", err)
		}
	}
	return nil
}

func mergePipelineConfig(pbCfg *pb.PipelineConfig, cfg domain.PipelineConfig, update bool) (domain.PipelineConfig, error) {
	if pbCfg == nil {
		return cfg, nil
	}
	if pbCfg.Parsing != nil {
		parsing := domain.ParsingConfig{
			Engines: pbCfg.Parsing.Engines,
			VLM:     cfg.Parsing.VLM,
		}
		if pbCfg.Parsing.MineruPrecision != nil {
			parsing.MinerUPrecision = &domain.MinerUConfig{
				APIURL:   pbCfg.Parsing.MineruPrecision.ApiUrl,
				APIToken: pbCfg.Parsing.MineruPrecision.ApiToken,
				APIKey:   pbCfg.Parsing.MineruPrecision.ApiKey,
			}
		}
		if pbCfg.Parsing.MineruAgent != nil {
			parsing.MinerUAgent = &domain.MinerUConfig{
				APIURL:   pbCfg.Parsing.MineruAgent.ApiUrl,
				APIToken: pbCfg.Parsing.MineruAgent.ApiToken,
				APIKey:   pbCfg.Parsing.MineruAgent.ApiKey,
			}
		}
		if vlm := pbCfg.Parsing.Vlm; vlm != nil {
			if err := validateModelReference(vlm.ModelId, vlm.ClearModelId, update); err != nil {
				return domain.PipelineConfig{}, err
			}
			var modelID *string
			if cfg.Parsing.VLM != nil {
				modelID = cfg.Parsing.VLM.ModelID
			}
			if vlm.ClearModelId {
				modelID = nil
			} else if vlm.ModelId != nil {
				modelID = vlm.ModelId
			}
			parsing.VLM = &domain.VLMConfig{
				Enabled:  vlm.Enabled,
				ModelID:  modelID,
				Provider: vlm.Provider,
				Model:    vlm.Model,
				APIKey:   vlm.ApiKey,
				BaseURL:  vlm.BaseUrl,
			}
		}
		cfg.Parsing = parsing
	}
	if pbCfg.Chunking != nil {
		cfg.Chunking = domain.ChunkingConfig{
			ChunkSize:  int(pbCfg.Chunking.ChunkSize),
			Overlap:    int(pbCfg.Chunking.Overlap),
			Separators: pbCfg.Chunking.Separators,
		}
		if pbCfg.Chunking.ParentChild != nil {
			cfg.Chunking.ParentChild = domain.ParentChildConfig{
				Enabled:    pbCfg.Chunking.ParentChild.Enabled,
				ParentSize: int(pbCfg.Chunking.ParentChild.ParentSize),
				ChildSize:  int(pbCfg.Chunking.ParentChild.ChildSize),
			}
		}
	}
	if pbCfg.Retrieval != nil {
		rerank := cfg.Retrieval.Rerank
		if pbRerank := pbCfg.Retrieval.Rerank; pbRerank != nil {
			if err := validateModelReference(pbRerank.ModelId, pbRerank.ClearModelId, update); err != nil {
				return domain.PipelineConfig{}, err
			}
			rerank.Enabled, rerank.TopN = pbRerank.Enabled, int(pbRerank.TopN)
			if pbRerank.ClearModelId {
				rerank.ModelID = nil
			} else if pbRerank.ModelId != nil {
				rerank.ModelID = pbRerank.ModelId
			}
		}
		cfg.Retrieval = domain.RetrievalConfig{
			Mode:           pbCfg.Retrieval.Mode,
			CandidateTopK:  int(pbCfg.Retrieval.CandidateTopK),
			ScoreThreshold: pbCfg.Retrieval.ScoreThreshold,
			DenseWeight:    pbCfg.Retrieval.DenseWeight,
			SparseWeight:   pbCfg.Retrieval.SparseWeight,
			Rerank:         rerank,
		}
	}
	if err := cfg.ValidateModelReferences(); err != nil {
		return domain.PipelineConfig{}, errors.Wrap(errors.CodeInvalidParam, "invalid pipeline model reference", err)
	}
	return cfg, nil
}

func pipelineConfigToProto(cfg domain.PipelineConfig) *pb.PipelineConfig {
	if cfg.Chunking.ChunkSize == 0 && !cfg.Chunking.ParentChild.Enabled && cfg.Retrieval.Mode == "" && len(cfg.Parsing.Engines) == 0 && cfg.Parsing.MinerUPrecision == nil && cfg.Parsing.MinerUAgent == nil && cfg.Parsing.VLM == nil && cfg.Retrieval.Rerank.ModelID == nil && !cfg.Retrieval.Rerank.Enabled {
		return nil
	}
	pbCfg := &pb.PipelineConfig{}
	if len(cfg.Parsing.Engines) > 0 || cfg.Parsing.MinerUPrecision != nil || cfg.Parsing.MinerUAgent != nil || cfg.Parsing.VLM != nil {
		parsing := &pb.ParsingConfig{
			Engines: cfg.Parsing.Engines,
		}
		if cfg.Parsing.MinerUPrecision != nil {
			parsing.MineruPrecision = &pb.MinerUConfig{
				ApiUrl:   cfg.Parsing.MinerUPrecision.APIURL,
				ApiToken: cfg.Parsing.MinerUPrecision.APIToken,
				ApiKey:   cfg.Parsing.MinerUPrecision.APIKey,
			}
		}
		if cfg.Parsing.MinerUAgent != nil {
			parsing.MineruAgent = &pb.MinerUConfig{
				ApiUrl:   cfg.Parsing.MinerUAgent.APIURL,
				ApiToken: cfg.Parsing.MinerUAgent.APIToken,
				ApiKey:   cfg.Parsing.MinerUAgent.APIKey,
			}
		}
		if cfg.Parsing.VLM != nil {
			parsing.Vlm = &pb.VLMConfig{
				Enabled:  cfg.Parsing.VLM.Enabled,
				ModelId:  cfg.Parsing.VLM.ModelID,
				Provider: cfg.Parsing.VLM.Provider,
				Model:    cfg.Parsing.VLM.Model,
				ApiKey:   cfg.Parsing.VLM.APIKey,
				BaseUrl:  cfg.Parsing.VLM.BaseURL,
			}
		}
		pbCfg.Parsing = parsing
	}
	if cfg.Chunking.ChunkSize > 0 || cfg.Chunking.ParentChild.Enabled {
		chunkPB := &pb.ChunkingConfig{
			ChunkSize:  int32(cfg.Chunking.ChunkSize),
			Overlap:    int32(cfg.Chunking.Overlap),
			Separators: cfg.Chunking.Separators,
		}
		if cfg.Chunking.ParentChild.Enabled {
			chunkPB.ParentChild = &pb.ParentChildConfig{
				Enabled:    true,
				ParentSize: int32(cfg.Chunking.ParentChild.ParentSize),
				ChildSize:  int32(cfg.Chunking.ParentChild.ChildSize),
			}
		}
		pbCfg.Chunking = chunkPB
	}
	if cfg.Retrieval.Mode != "" || cfg.Retrieval.Rerank.ModelID != nil || cfg.Retrieval.Rerank.Enabled {
		retPB := &pb.RetrievalConfig{
			Mode:           cfg.Retrieval.Mode,
			CandidateTopK:  int32(cfg.Retrieval.CandidateTopK),
			ScoreThreshold: cfg.Retrieval.ScoreThreshold,
			DenseWeight:    cfg.Retrieval.DenseWeight,
			SparseWeight:   cfg.Retrieval.SparseWeight,
		}
		if cfg.Retrieval.Rerank.Enabled || cfg.Retrieval.Rerank.ModelID != nil {
			retPB.Rerank = &pb.RerankConfig{
				Enabled: cfg.Retrieval.Rerank.Enabled,
				ModelId: cfg.Retrieval.Rerank.ModelID,
				TopN:    int32(cfg.Retrieval.Rerank.TopN),
			}
		}
		pbCfg.Retrieval = retPB
	}
	return pbCfg
}
