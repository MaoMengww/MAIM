package handler

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
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

// ========== Wiki ==========

func (h *KnowledgeHandler) WikiListPages(c *gin.Context) {
	kbID := parseInt64(c.Param("id"))
	req := &kbpb.WikiListPagesReq{
		KbId:     kbID,
		PageType: c.DefaultQuery("page_type", ""),
		Limit:    int32(parseInt64(c.DefaultQuery("limit", "0"))),
		Offset:   int32(parseInt64(c.DefaultQuery("offset", "0"))),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiListPages(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiReadIndex(c *gin.Context) {
	req := &kbpb.WikiReadIndexReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiReadIndex(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiReadPage(c *gin.Context) {
	rawSlug := c.Param("slug")
	req := &kbpb.WikiReadPageReq{
		KbId: parseInt64(c.Param("id")),
		Slug: strings.TrimLeft(rawSlug, "/"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiReadPage(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiSearch(c *gin.Context) {
	req := &kbpb.WikiSearchReq{
		KbId:  parseInt64(c.Param("id")),
		Query: c.Query("q"),
		Limit: int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiSearch(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiUpdatePage(c *gin.Context) {
	var body struct {
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Summary string   `json:"summary"`
		Aliases []string `json:"aliases"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	rawSlug := c.Param("slug")
	req := &kbpb.WikiUpdatePageReq{
		KbId:    parseInt64(c.Param("id")),
		Slug:    strings.TrimLeft(rawSlug, "/"),
		Title:   body.Title,
		Content: body.Content,
		Summary: body.Summary,
		Aliases: body.Aliases,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiUpdatePage(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiDeletePage(c *gin.Context) {
	rawSlug := c.Param("slug")
	req := &kbpb.WikiDeletePageReq{
		KbId: parseInt64(c.Param("id")),
		Slug: strings.TrimLeft(rawSlug, "/"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.client.WikiDeletePage(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *KnowledgeHandler) WikiListIssues(c *gin.Context) {
	kbID := parseInt64(c.Param("id"))
	status := c.Query("status")
	slug := c.Query("slug")
	issueIDStr := c.Query("issue_id")

	conn := h.client
	ctx := middleware.WithGRPCMetadata(c)

	// If issue_id is provided, call WikiReadIssue
	if issueIDStr != "" {
		issueID, _ := strconv.ParseInt(issueIDStr, 10, 64)
		resp, err := conn.WikiReadIssue(ctx, &kbpb.WikiReadIssueReq{
			KbId: kbID, Slug: slug, IssueId: issueID,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		response.Success(c, resp)
		return
	}

	// Otherwise list issues
	resp, err := conn.WikiListIssues(ctx, &kbpb.WikiListIssuesReq{
		KbId: kbID, Status: status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiRefresh(c *gin.Context) {
	req := &kbpb.WikiRefreshReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiRefresh(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiRunMaintenance(c *gin.Context) {
	req := &kbpb.WikiMaintenanceReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiRunMaintenance(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiGraph(c *gin.Context) {
	req := &kbpb.WikiGraphReq{KbId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiGraph(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Wiki — New tools ==========

func (h *KnowledgeHandler) WikiReadSourceDoc(c *gin.Context) {
	req := &kbpb.WikiReadSourceDocReq{
		KbId:  parseInt64(c.Param("id")),
		DocId: parseInt64(c.Param("docId")),
		Query: c.Query("query"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiReadSourceDoc(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiReplaceText(c *gin.Context) {
	var body struct {
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req := &kbpb.WikiReplaceTextReq{
		KbId:    parseInt64(c.Param("id")),
		Slug:    c.Param("slug"),
		OldText: body.OldText,
		NewText: body.NewText,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiReplaceText(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiRenamePage(c *gin.Context) {
	var body struct {
		NewSlug string `json:"new_slug"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req := &kbpb.WikiRenamePageReq{
		KbId:    parseInt64(c.Param("id")),
		Slug:    c.Param("slug"),
		NewSlug: body.NewSlug,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiRenamePage(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiFlagIssue(c *gin.Context) {
	var body struct {
		Slug        string `json:"slug"`
		IssueType   string `json:"issue_type"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req := &kbpb.WikiFlagIssueReq{
		KbId:        parseInt64(c.Param("id")),
		Slug:        body.Slug,
		IssueType:   body.IssueType,
		Description: body.Description,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiFlagIssue(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiReadIssue(c *gin.Context) {
	issueID, _ := strconv.ParseInt(c.Query("issue_id"), 10, 64)
	req := &kbpb.WikiReadIssueReq{
		KbId:    parseInt64(c.Param("id")),
		Slug:    c.Query("slug"),
		IssueId: issueID,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiReadIssue(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiUpdateIssue(c *gin.Context) {
	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	issueID, _ := strconv.ParseInt(c.Param("issueId"), 10, 64)
	req := &kbpb.WikiUpdateIssueReq{
		IssueId: issueID,
		Status:  body.Status,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.client.WikiUpdateIssue(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *KnowledgeHandler) WikiBatchUploadDocuments(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	ctx = metadata.AppendToOutgoingContext(ctx, "kb-id", c.Param("id"))

	// Parse multipart form
	form, err := c.MultipartForm()
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		response.BadRequest(c, "no files provided")
		return
	}

	stream, err := h.client.WikiBatchUploadDocuments(ctx)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// Send meta
	var metas []*kbpb.FileMeta
	for _, f := range files {
		metas = append(metas, &kbpb.FileMeta{
			Title:            f.Filename,
			FileType:         fileExt(f.Filename),
			FileSize:         f.Size,
			OriginalFilename: f.Filename,
		})
	}
	_ = stream.Send(&kbpb.WikiBatchUploadDocumentReq{
		Data: &kbpb.WikiBatchUploadDocumentReq_Meta{
			Meta: &kbpb.WikiBatchMetaChunk{Files: metas},
		},
	})

	// Send file contents
	for _, f := range files {
		content, err := f.Open()
		if err != nil {
			continue
		}
		buf := new(bytes.Buffer)
		_, _ = io.Copy(buf, content)
		_ = content.Close()
		_ = stream.Send(&kbpb.WikiBatchUploadDocumentReq{
			Data: &kbpb.WikiBatchUploadDocumentReq_Content{
				Content: buf.Bytes(),
			},
		})
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// fileExt returns a lowercase file extension for supported types.
func fileExt(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	switch ext {
	case "pdf", "md", "txt", "docx", "html", "csv":
		return ext
	default:
		return "txt"
	}
}
