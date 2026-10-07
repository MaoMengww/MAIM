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
	if err := bindUserOwnedJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
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
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListKBs(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetKB(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.GetKBReq{KbId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.GetKB(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) UpdateKB(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req kbpb.UpdateKBReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.KbId = c.Param("id")
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.client.UpdateKB(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) DeleteKB(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.DeleteKBReq{KbId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	_, err := h.client.DeleteKB(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

// ========== Document ==========

func (h *KnowledgeHandler) UploadDocument(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
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
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.ListDocumentsReq{
		KbId:   c.Param("id"),
		Offset: int32(parseInt64(c.DefaultQuery("offset", "0"))),
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
		Status: c.Query("status"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListDocuments(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetDocument(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.GetDocumentReq{DocId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.GetDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) DeleteDocument(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.DeleteDocumentReq{DocId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	_, err := h.client.DeleteDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) RetryDocument(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.RetryDocumentReq{DocId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.RetryDocument(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) GetDocumentContent(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.GetDocumentContentReq{DocId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.GetDocumentContent(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) ListChunks(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.ListChunksReq{
		DocId:  c.Param("id"),
		Offset: int32(parseInt64(c.DefaultQuery("offset", "0"))),
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListChunks(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Binding (Bot) ==========

func (h *KnowledgeHandler) BindToBot(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req kbpb.BindReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.TargetType = "bot"
	req.TargetId = c.Param("id")
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	_, err := h.client.Bind(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) UnbindFromBot(c *gin.Context) {
	if !requirePathIdentities(c, "kid", "id") {
		return
	}
	req := &kbpb.UnbindReq{
		KbId:       c.Param("kid"),
		TargetType: "bot",
		TargetId:   c.Param("id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	_, err := h.client.Unbind(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) ListBotBindings(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.ListBindingsReq{
		TargetType: "bot",
		TargetId:   c.Param("id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListBindings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Binding (Conv) ==========

func (h *KnowledgeHandler) BindToConv(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req kbpb.BindReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.TargetType = "conv"
	req.TargetId = c.Param("id")
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	_, err := h.client.Bind(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) UnbindFromConv(c *gin.Context) {
	if !requirePathIdentities(c, "kid", "id") {
		return
	}
	req := &kbpb.UnbindReq{
		KbId:       c.Param("kid"),
		TargetType: "conv",
		TargetId:   c.Param("id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	_, err := h.client.Unbind(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) ListConvBindings(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.ListBindingsReq{
		TargetType: "conv",
		TargetId:   c.Param("id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListBindings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== KB Bindings (reverse) ==========

func (h *KnowledgeHandler) ListKBBindings(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &kbpb.ListBoundTargetsReq{KbId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.client.ListBoundTargets(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Search ==========

func (h *KnowledgeHandler) Search(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req kbpb.RetrieveReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.KbIds = []string{c.Param("id")}
	// The direct KB route must not resolve a different scope through target bindings.
	req.BotId = nil
	req.ConvId = nil
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.client.Retrieve(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
