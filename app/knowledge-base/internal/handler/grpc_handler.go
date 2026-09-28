package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/parser"
	"github.com/maomeng/aim/app/knowledge-base/internal/metrics"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	llmgatewaypb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type KnowledgeBaseHandler struct {
	pb.UnimplementedKnowledgeBaseServer
	KBRepo         domain.KBRepo
	DocRepo        domain.DocumentRepo
	FileStore      domain.FileStore
	VectorStore    domain.VectorStore
	Producer       *kafka.Producer
	IngestPipeline *pipeline.IngestPipeline
	RetrievePipe   *pipeline.RetrievePipeline
	Snowflake      *snowflake.Node
	Logger         logx.Logger
	LLMGateway     zrpc.Client
}

type PipelineConfigProvider interface {
	GetPipelineConfig(kbID int64) (domain.PipelineConfig, error)
}

func (h *KnowledgeBaseHandler) CreateKB(ctx context.Context, req *pb.CreateKBReq) (*pb.KBRsp, error) {
	ownerID := getCallerID(ctx)
	if ownerID == 0 {
		return nil, errors.ErrUnauthorized
	}
	cfg := convertPipelineConfig(req.PipelineConfig)
	if cfg.Parsing.VLM != nil && cfg.Parsing.VLM.ModelID > 0 && h.LLMGateway != nil {
		conn := h.LLMGateway.Conn()
		if conn != nil {
			cli := llmgatewaypb.NewLLMGatewayClient(conn)
			models, err := cli.ListModels(ctx, &llmgatewaypb.ListModelsReq{})
			if err == nil {
				for _, m := range models.Items {
					if m.Id == cfg.Parsing.VLM.ModelID {
						cfg.Parsing.VLM.Provider = m.Provider
						cfg.Parsing.VLM.Model = m.ModelName
						cfg.Parsing.VLM.BaseURL = m.BaseUrl
						cfg.Parsing.VLM.APIKey = m.ApiKey
						cfg.Parsing.VLM.Enabled = true
						break
					}
				}
			}
		}
	}
	kbID, err := h.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate kb id failed: %w", err)
	}
	kb := &domain.KnowledgeBase{
		ID:               kbID,
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
	h.Logger.WithContext(ctx).Infof("knowledge base created: kb_id=%d name=%s owner_id=%d", kb.ID, kb.Name, ownerID)
	return toKBRsp(kb), nil
}

func (h *KnowledgeBaseHandler) UpdateKB(ctx context.Context, req *pb.UpdateKBReq) (*pb.KBRsp, error) {
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, req.KbId)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	if kb.OwnerID != callerID && !isAdmin(ctx) {
		return nil, domain.ErrForbidden
	}
	if req.GetName() != "" {
		kb.Name = req.GetName()
	}
	if req.GetDescription() != "" {
		kb.Description = req.GetDescription()
	}
	if req.GetEmbeddingModel() != "" {
		kb.EmbeddingModel = req.GetEmbeddingModel()
	}
	if req.GetEmbeddingModelId() > 0 {
		kb.EmbeddingModelID = req.GetEmbeddingModelId()
	}
	if req.GetPipelineConfig() != nil {
		kb.PipelineConfig = convertPipelineConfig(req.GetPipelineConfig())
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
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, req.KbId)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	if kb.OwnerID != callerID && !isAdmin(ctx) {
		return nil, domain.ErrForbidden
	}
	bindings, _ := h.KBRepo.ListBindingsByKB(ctx, req.KbId)
	for _, b := range bindings {
		_ = h.KBRepo.Unbind(ctx, req.KbId, b.TargetType, b.TargetID)
	}
	docs, _, _ := h.DocRepo.ListByKB(ctx, req.KbId, 0, 10000, "")
	for _, doc := range docs {
		_ = h.DocRepo.DeleteChunksByDoc(ctx, doc.ID)
		if doc.MinioKey != "" {
			_ = h.FileStore.Delete(ctx, doc.MinioKey)
		}
		if h.VectorStore != nil {
			_ = h.VectorStore.DeleteByDoc(ctx, doc.ID)
		}
		_ = h.DocRepo.Delete(ctx, doc.ID)
	}
	if err := h.KBRepo.Delete(ctx, req.KbId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "delete kb failed", err)
	}
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) GetKB(ctx context.Context, req *pb.GetKBReq) (*pb.KBRsp, error) {
	kb, err := h.KBRepo.Get(ctx, req.KbId)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	return toKBRsp(kb), nil
}

func (h *KnowledgeBaseHandler) ListKBs(ctx context.Context, req *pb.ListKBsReq) (*pb.ListKBsRsp, error) {
	ownerID := getCallerID(ctx)
	offset, limit := int(req.Offset), int(req.Limit)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	kbs, total, err := h.KBRepo.ListByOwner(ctx, ownerID, offset, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list kbs failed", err)
	}
	items := make([]*pb.KBRsp, len(kbs))
	for i, kb := range kbs {
		items[i] = toKBRsp(&kb)
	}
	h.Logger.WithContext(ctx).Infof("knowledge bases listed: user_id=%d count=%d", ownerID, total)
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
	if kbID == 0 {
		return errors.ErrInvalidParam
	}
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, kbID)
	if err != nil {
		return domain.ErrKBNotFound
	}
	if callerID > 0 && kb.OwnerID != callerID && !isAdmin(ctx) {
		return domain.ErrForbidden
	}
	contentHash := fmt.Sprintf("%x", sha256.Sum256(fileBytes.Bytes()))

	// Check for duplicate content within the same KB
	if existing, err := h.DocRepo.GetByHash(ctx, kbID, contentHash); err == nil && existing != nil {
		return errors.New(errors.CodeConflict, "file already exists in this knowledge base")
	}

	minioKey := fmt.Sprintf("knowledge/%d/%s/%s", kbID, contentHash, meta.OriginalFilename)
	if err := h.FileStore.Put(ctx, minioKey, bytes.NewReader(fileBytes.Bytes()), meta.FileSize, contentType(meta.FileType)); err != nil {
		return errors.Wrap(errors.CodeIOError, "upload file failed", err)
	}
	pipelineOverride := convertPipelineConfig(meta.PipelineOverride)
	// fileType is already validated above
	docID, err := h.Snowflake.Generate()
	if err != nil {
		return fmt.Errorf("generate doc id failed: %w", err)
	}
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
		PipelineOverride: &pipelineOverride,
		Metadata:         parseJSONMeta(meta.Metadata),
	}
	if err := h.DocRepo.Create(ctx, doc); err != nil {
		return errors.Wrap(errors.CodeDBError, "create document failed", err)
	}
	if err := h.KBRepo.UpdateCounts(ctx, kbID); err != nil {
		h.Logger.WithContext(ctx).Errorf("failed to update kb counts after upload: %v", err)
	}
	eventData, err := json.Marshal(map[string]any{
		"doc_id": doc.ID,
	})
	if err != nil {
		return errors.Wrap(errors.CodeInternal, "marshal upload event failed", err)
	}
	if err := h.Producer.Send(ctx, fmt.Sprintf("%d", doc.ID), eventData); err != nil {
		return errors.Wrap(errors.CodeMQError, "publish upload event failed", err)
	}
	h.Logger.WithContext(ctx).Infof("document uploaded: doc_id=%d kb_id=%d file_name=%s", doc.ID, kbID, meta.OriginalFilename)
	return stream.SendAndClose(toDocumentRsp(doc))
}

func (h *KnowledgeBaseHandler) GetDocument(ctx context.Context, req *pb.GetDocumentReq) (*pb.DocumentRsp, error) {
	doc, err := h.DocRepo.Get(ctx, req.DocId)
	if err != nil {
		return nil, domain.ErrDocNotFound
	}
	return toDocumentRsp(doc), nil
}

func (h *KnowledgeBaseHandler) ListDocuments(ctx context.Context, req *pb.ListDocumentsReq) (*pb.ListDocumentsRsp, error) {
	offset, limit := int(req.Offset), int(req.Limit)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	docs, total, err := h.DocRepo.ListByKB(ctx, req.KbId, offset, limit, req.Status)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list documents failed", err)
	}
	items := make([]*pb.DocumentRsp, len(docs))
	for i, doc := range docs {
		items[i] = toDocumentRsp(&doc)
	}
	return &pb.ListDocumentsRsp{Items: items, Total: total}, nil
}

func (h *KnowledgeBaseHandler) GetDocumentContent(ctx context.Context, req *pb.GetDocumentContentReq) (*pb.GetDocumentContentResp, error) {
	doc, err := h.DocRepo.Get(ctx, req.DocId)
	if err != nil {
		return nil, domain.ErrDocNotFound
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
	defer reader.Close()
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
	doc, err := h.DocRepo.Get(ctx, req.DocId)
	if err != nil {
		return nil, domain.ErrDocNotFound
	}
	offset, limit := int(req.Offset), int(req.Limit)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	total, err := h.DocRepo.CountChunksByDocID(ctx, doc.ID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "failed to count chunks", err)
	}
	chunks, err := h.DocRepo.GetChunksByDocID(ctx, doc.ID, offset, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "failed to list chunks", err)
	}
	pbChunks := make([]*pb.ChunkInfo, len(chunks))
	for i, ch := range chunks {
		var metadata string
		if len(ch.Metadata) > 0 {
			metaBytes, _ := json.Marshal(ch.Metadata)
			metadata = string(metaBytes)
		}
		pbChunks[i] = &pb.ChunkInfo{
			Id:         ch.ID,
			DocId:      ch.DocID,
			ChunkIndex: int32(ch.ChunkIndex),
			Content:    ch.Content,
			TokenCount: int32(ch.TokenCount),
			Metadata:   metadata,
			CreatedAt:  ch.CreatedAt.Unix(),
		}
	}
	return &pb.ListChunksResp{Chunks: pbChunks, Total: total}, nil
}

func (h *KnowledgeBaseHandler) DeleteDocument(ctx context.Context, req *pb.DeleteDocumentReq) (*emptypb.Empty, error) {
	logger := h.Logger.WithContext(ctx)
	doc, err := h.DocRepo.Get(ctx, req.DocId)
	if err != nil {
		return nil, domain.ErrDocNotFound
	}
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, doc.KBID)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	if kb.OwnerID != callerID && !isAdmin(ctx) {
		return nil, domain.ErrForbidden
	}
	if doc.Status != domain.DocStatusReady && doc.Status != domain.DocStatusFailed {
		return nil, domain.ErrDocumentProcessing
	}
	if doc.MinioKey != "" {
		_ = h.FileStore.Delete(ctx, doc.MinioKey)
	}
	if h.VectorStore != nil {
		_ = h.VectorStore.DeleteByDoc(ctx, doc.ID)
	}
	_ = h.DocRepo.DeleteChunksByDoc(ctx, doc.ID)
	if err := h.DocRepo.Delete(ctx, doc.ID); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "delete document failed", err)
	}
	if err := h.KBRepo.UpdateCounts(ctx, doc.KBID); err != nil {
		logger.Errorf("failed to update kb counts after delete: %v", err)
	}
	logger.Infof("document deleted: doc_id=%d kb_id=%d", doc.ID, doc.KBID)
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) RetryDocument(ctx context.Context, req *pb.RetryDocumentReq) (*pb.DocumentRsp, error) {
	doc, err := h.DocRepo.Get(ctx, req.DocId)
	if err != nil {
		return nil, domain.ErrDocNotFound
	}
	if doc.Status != domain.DocStatusFailed {
		return nil, domain.ErrDocumentNotFailed
	}
	doc.Status = domain.DocStatusPending
	doc.ErrorMessage = ""
	_ = h.DocRepo.Update(ctx, doc)
	eventData, err := json.Marshal(map[string]any{
		"doc_id": doc.ID,
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "marshal retry event failed", err)
	}
	if err := h.Producer.Send(ctx, fmt.Sprintf("%d", doc.ID), eventData); err != nil {
		return nil, errors.Wrap(errors.CodeMQError, "publish retry event failed", err)
	}
	return toDocumentRsp(doc), nil
}

func (h *KnowledgeBaseHandler) Retrieve(ctx context.Context, req *pb.RetrieveReq) (*pb.RetrieveRsp, error) {
	var kbIDs []int64
	if len(req.KbIds) > 0 {
		kbIDs = req.KbIds
	} else {
		if req.BotId > 0 {
			bindings, err := h.KBRepo.ListBindingsByTarget(ctx, "bot", req.BotId)
			if err == nil {
				for _, b := range bindings {
					if kb, err := h.KBRepo.Get(ctx, b.KBID); err == nil && kb.Mode == "rag" {
						kbIDs = append(kbIDs, b.KBID)
					}
				}
			}
		}
		if req.ConvId > 0 {
			bindings, err := h.KBRepo.ListBindingsByTarget(ctx, "conv", req.ConvId)
			if err == nil {
				for _, b := range bindings {
					if kb, err := h.KBRepo.Get(ctx, b.KBID); err == nil && kb.Mode == "rag" {
						kbIDs = append(kbIDs, b.KBID)
					}
				}
			}
		}
	}
	if len(kbIDs) == 0 {
		h.Logger.WithContext(ctx).Infof("retrieve: kb_ids=[] query_len=%d doc_count=0", len(req.Query))
		return &pb.RetrieveRsp{}, nil
	}
	kb, err := h.KBRepo.Get(ctx, kbIDs[0])
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	retrievalCfg := kb.PipelineConfig.Retrieval
	metrics.KbSearchTotal.Inc("rag")
	if h.RetrievePipe != nil {
		items, err := h.RetrievePipe.Retrieve(ctx, req.BotId, req.ConvId, req.Query, retrievalCfg, kb.OwnerID)
		if err != nil {
			return nil, err
		}
		pbItems := make([]*pb.RetrieveItem, len(items))
		for i, item := range items {
			pbItems[i] = &pb.RetrieveItem{
				Content:        item.Content,
				Score:          item.Score,
				DocId:          item.DocID,
				DocTitle:       item.DocTitle,
				KbId:           item.KBID,
				KbName:         item.KBName,
				MatchedContent: item.MatchedContent,
			}
		}
		h.Logger.WithContext(ctx).Infof("retrieve: kb_id=%d query_len=%d doc_count=%d", kbIDs[0], len(req.Query), len(pbItems))
		return &pb.RetrieveRsp{Items: pbItems}, nil
	}
	h.Logger.WithContext(ctx).Infof("retrieve: kb_id=%d query_len=%d doc_count=0 (no pipeline)", kbIDs[0], len(req.Query))
	return &pb.RetrieveRsp{}, nil
}

func (h *KnowledgeBaseHandler) Bind(ctx context.Context, req *pb.BindReq) (*emptypb.Empty, error) {
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, req.KbId)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	if kb.OwnerID != callerID && !isAdmin(ctx) {
		return nil, domain.ErrForbidden
	}
	bindingID, err := h.Snowflake.Generate()
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
	h.Logger.WithContext(ctx).Infof("kb bound: kb_id=%d target_type=%s target_id=%d", req.KbId, req.TargetType, req.TargetId)
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) Unbind(ctx context.Context, req *pb.UnbindReq) (*emptypb.Empty, error) {
	callerID := getCallerID(ctx)
	kb, err := h.KBRepo.Get(ctx, req.KbId)
	if err != nil {
		return nil, domain.ErrKBNotFound
	}
	if kb.OwnerID != callerID && !isAdmin(ctx) {
		return nil, domain.ErrForbidden
	}
	if err := h.KBRepo.Unbind(ctx, req.KbId, req.TargetType, req.TargetId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "unbind failed", err)
	}
	h.Logger.WithContext(ctx).Infof("kb unbound: kb_id=%d target_type=%s target_id=%d", req.KbId, req.TargetType, req.TargetId)
	return &emptypb.Empty{}, nil
}

func (h *KnowledgeBaseHandler) ListBindings(ctx context.Context, req *pb.ListBindingsReq) (*pb.ListBindingsRsp, error) {
	bindings, err := h.KBRepo.ListBindingsByTarget(ctx, req.TargetType, req.TargetId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list bindings failed", err)
	}
	items := make([]*pb.BindingItem, len(bindings))
	for i, b := range bindings {
		mode := "rag"
		if kb, err := h.KBRepo.Get(ctx, b.KBID); err == nil {
			mode = kb.Mode
		}
		items[i] = &pb.BindingItem{
			Id:         b.ID,
			KbId:       b.KBID,
			KbName:     b.KBName,
			Mode:       mode,
			TargetType: b.TargetType,
			TargetId:   b.TargetID,
			CreatedAt:  b.CreatedAt.Unix(),
		}
	}
	return &pb.ListBindingsRsp{Items: items}, nil
}

func (h *KnowledgeBaseHandler) ListBoundTargets(ctx context.Context, req *pb.ListBoundTargetsReq) (*pb.ListBoundTargetsRsp, error) {
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

func getCallerID(ctx context.Context) int64 {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-user-id"); len(vals) > 0 {
			id, err := strconv.ParseInt(vals[0], 10, 64)
			if err == nil {
				return id
			}
		}
		if vals := md.Get("user-id"); len(vals) > 0 {
			id, err := strconv.ParseInt(vals[0], 10, 64)
			if err == nil {
				return id
			}
		}
	}
	return 0
}

func getKBIDFromContext(ctx context.Context) int64 {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("kb-id"); len(vals) > 0 {
			id, _ := strconv.ParseInt(vals[0], 10, 64)
			return id
		}
	}
	return 0
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
	return rsp
}

func convertPipelineConfig(pbCfg *pb.PipelineConfig) domain.PipelineConfig {
	if pbCfg == nil {
		return domain.PipelineConfig{}
	}
	cfg := domain.PipelineConfig{}
	if pbCfg.Parsing != nil {
		parsing := domain.ParsingConfig{
			Engines: pbCfg.Parsing.Engines,
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
		if pbCfg.Parsing.Vlm != nil {
			parsing.VLM = &domain.VLMConfig{
				Enabled:  pbCfg.Parsing.Vlm.Enabled,
				ModelID:  pbCfg.Parsing.Vlm.ModelId,
				Provider: pbCfg.Parsing.Vlm.Provider,
				Model:    pbCfg.Parsing.Vlm.Model,
				APIKey:   pbCfg.Parsing.Vlm.ApiKey,
				BaseURL:  pbCfg.Parsing.Vlm.BaseUrl,
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
		cfg.Retrieval = domain.RetrievalConfig{
			Mode:           pbCfg.Retrieval.Mode,
			TopK:           int(pbCfg.Retrieval.TopK),
			CandidateTopK:  int(pbCfg.Retrieval.CandidateTopK),
			ScoreThreshold: pbCfg.Retrieval.ScoreThreshold,
			DenseWeight:    pbCfg.Retrieval.DenseWeight,
			SparseWeight:   pbCfg.Retrieval.SparseWeight,
		}
		if pbCfg.Retrieval.Rerank != nil {
			cfg.Retrieval.Rerank = domain.RerankConfig{
				Enabled: pbCfg.Retrieval.Rerank.Enabled,
				ModelID: pbCfg.Retrieval.Rerank.ModelId,
				TopN:    int(pbCfg.Retrieval.Rerank.TopN),
			}
		}
	}
	return cfg
}

func pipelineConfigToProto(cfg domain.PipelineConfig) *pb.PipelineConfig {
	if cfg.Chunking.ChunkSize == 0 && !cfg.Chunking.ParentChild.Enabled && cfg.Retrieval.Mode == "" && len(cfg.Parsing.Engines) == 0 {
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
	if cfg.Retrieval.Mode != "" {
		retPB := &pb.RetrievalConfig{
			Mode:           cfg.Retrieval.Mode,
			TopK:           int32(cfg.Retrieval.TopK),
			CandidateTopK:  int32(cfg.Retrieval.CandidateTopK),
			ScoreThreshold: cfg.Retrieval.ScoreThreshold,
			DenseWeight:    cfg.Retrieval.DenseWeight,
			SparseWeight:   cfg.Retrieval.SparseWeight,
		}
		if cfg.Retrieval.Rerank.Enabled {
			retPB.Rerank = &pb.RerankConfig{
				Enabled: true,
				ModelId: cfg.Retrieval.Rerank.ModelID,
				TopN:    int32(cfg.Retrieval.Rerank.TopN),
			}
		}
		pbCfg.Retrieval = retPB
	}
	return pbCfg
}
