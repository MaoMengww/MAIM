package handler

import (
	"crypto/sha256"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	kbpb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type KnowledgeHandler struct {
	client kbpb.KnowledgeBaseClient
}

func NewKnowledgeHandler(conn *grpc.ClientConn) *KnowledgeHandler {
	return &KnowledgeHandler{
		client: kbpb.NewKnowledgeBaseClient(conn),
	}
}

// ========== KB CRUD ==========

func (h *KnowledgeHandler) CreateKB(c *gin.Context) {
	var req kbpb.CreateKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.CreateKB(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) ListKBs(c *gin.Context) {
	req := &kbpb.ListKBsReq{
		Offset: int32(parseInt64(c.DefaultQuery("offset", "0"))),
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListKBs(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetKB(c *gin.Context) {
	req := &kbpb.GetKBReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.GetKB(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) UpdateKB(c *gin.Context) {
	var req kbpb.UpdateKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.KbId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.UpdateKB(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) DeleteKB(c *gin.Context) {
	req := &kbpb.DeleteKBReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.DeleteKB(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

// ========== Document ==========

func (h *KnowledgeHandler) UploadDocument(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	ctx = metadata.AppendToOutgoingContext(ctx, "kb-id", c.Param("id"))

	stream, err := h.client.UploadDocument(ctx)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "missing file")
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		response.BadRequest(c, "failed to read file")
		return
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(fileBytes))
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))

	meta := &kbpb.UploadDocumentReq_MetaChunk{
		Title:            c.PostForm("title"),
		FileType:         ext,
		FileSize:         header.Size,
		OriginalFilename: header.Filename,
		ContentHash:      hash,
		Metadata:         c.PostForm("metadata"),
	}

	if err := stream.Send(&kbpb.UploadDocumentReq{Data: &kbpb.UploadDocumentReq_Meta{Meta: meta}}); err != nil {
		response.GRPCError(c, err)
		return
	}

	if err := stream.Send(&kbpb.UploadDocumentReq{Data: &kbpb.UploadDocumentReq_Content{Content: fileBytes}}); err != nil {
		response.GRPCError(c, err)
		return
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) ListDocuments(c *gin.Context) {
	req := &kbpb.ListDocumentsReq{
		KbId:   parseInt64(c.Param("id")),
		Offset: int32(parseInt64(c.DefaultQuery("offset", "0"))),
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
		Status: c.Query("status"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListDocuments(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetDocument(c *gin.Context) {
	req := &kbpb.GetDocumentReq{DocId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.GetDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) DeleteDocument(c *gin.Context) {
	req := &kbpb.DeleteDocumentReq{DocId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.DeleteDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) RetryDocument(c *gin.Context) {
	req := &kbpb.RetryDocumentReq{DocId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.RetryDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetDocumentContent(c *gin.Context) {
	req := &kbpb.GetDocumentContentReq{DocId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.GetDocumentContent(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) ListChunks(c *gin.Context) {
	req := &kbpb.ListChunksReq{
		DocId:  parseInt64(c.Param("id")),
		Offset: int32(parseInt64(c.DefaultQuery("offset", "0"))),
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListChunks(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Binding (Bot) ==========

func (h *KnowledgeHandler) BindToBot(c *gin.Context) {
	var req kbpb.BindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.TargetType = "bot"
	req.TargetId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.Bind(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) UnbindFromBot(c *gin.Context) {
	req := &kbpb.UnbindReq{
		KbId:       parseInt64(c.Param("kid")),
		TargetType: "bot",
		TargetId:   parseInt64(c.Param("id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.Unbind(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) ListBotBindings(c *gin.Context) {
	req := &kbpb.ListBindingsReq{
		TargetType: "bot",
		TargetId:   parseInt64(c.Param("id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListBindings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Binding (Conv) ==========

func (h *KnowledgeHandler) BindToConv(c *gin.Context) {
	var req kbpb.BindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.TargetType = "conv"
	req.TargetId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.Bind(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) UnbindFromConv(c *gin.Context) {
	req := &kbpb.UnbindReq{
		KbId:       parseInt64(c.Param("kid")),
		TargetType: "conv",
		TargetId:   parseInt64(c.Param("id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.Unbind(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) ListConvBindings(c *gin.Context) {
	req := &kbpb.ListBindingsReq{
		TargetType: "conv",
		TargetId:   parseInt64(c.Param("id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListBindings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== KB Bindings (reverse) ==========

func (h *KnowledgeHandler) ListKBBindings(c *gin.Context) {
	req := &kbpb.ListBoundTargetsReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.ListBoundTargets(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Search ==========

func (h *KnowledgeHandler) Search(c *gin.Context) {
	var req kbpb.RetrieveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.Retrieve(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
