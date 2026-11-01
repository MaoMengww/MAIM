package handler

import (
	"context"
	"crypto/subtle"
	stderrors "errors"
	"net/http"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	"github.com/maomeng/aim/app/llm-gateway/internal/metrics"
	appModel "github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/app/llm-gateway/internal/svc"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type LLMGatewayHandler struct {
	pb.UnimplementedLLMGatewayServer
	svcCtx *svc.ServiceContext
}

func NewLLMGatewayHandler(svcCtx *svc.ServiceContext) *LLMGatewayHandler {
	return &LLMGatewayHandler{svcCtx: svcCtx}
}

// resolveModel applies the same visibility contract as model selection to every call.
func (h *LLMGatewayHandler) resolveModel(ctx context.Context, modelID string, botID, ownerID *string, capability string) (*modelEntryInfo, error) {
	if identity.Validate(modelID) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid model_id")
	}
	if err := validateInvocation(ctx, botID, ownerID); err != nil {
		return nil, err
	}
	entry, err := h.svcCtx.ModelRepo.FindByID(ctx, modelID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, errors.Wrap(errors.CodeDBError, "find model by id failed", err)
	}
	if entry == nil {
		return nil, errors.New(errors.CodeNotFound, "model not found")
	}
	if entry.Status != "active" || !modelVisible(entry.OwnerType, entry.OwnerID, ownerID) {
		return nil, errors.New(errors.CodeForbidden, "model unavailable")
	}
	if entry.Capability != capability {
		return nil, errors.New(errors.CodeInvalidParam, "model capability mismatch")
	}
	return &modelEntryInfo{ModelEntry: entry, Provider: entry.Provider, TrackBilling: entry.OwnerType == "platform"}, nil
}

func modelVisible(ownerType string, ownerID, requestedOwner *string) bool {
	switch ownerType {
	case "platform":
		return ownerID == nil
	case "user":
		return ownerID != nil && requestedOwner != nil && *ownerID == *requestedOwner
	default:
		return false
	}
}

func validateInvocation(ctx context.Context, botID, ownerID *string) error {
	for _, id := range []*string{botID, ownerID} {
		if id != nil && identity.Validate(*id) != nil {
			return errors.New(errors.CodeInvalidParam, "invalid entity reference")
		}
	}
	if caller, ok := ctx.Value(interceptor.ContextKeyUserID).(string); ok {
		if identity.Validate(caller) != nil {
			return errors.ErrUnauthorized
		}
		if ownerID == nil || *ownerID != caller {
			return errors.ErrForbidden
		}
	}
	return nil
}

func requireOwner(ctx context.Context, ownerID string) error {
	caller, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(caller) != nil {
		return errors.ErrUnauthorized
	}
	if identity.Validate(ownerID) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid owner_id")
	}
	if caller != ownerID {
		return errors.ErrForbidden
	}
	return nil
}

// ----- Balance check for official models -----

func (h *LLMGatewayHandler) checkBalance(ctx context.Context, ownerID *string) error {
	if ownerID == nil {
		return nil
	}
	ctx = balanceContext(ctx, *ownerID)
	resp, err := h.svcCtx.UserClient.GetBalance(ctx, &userpb.GetBalanceReq{UserId: *ownerID})
	if err != nil {
		return err
	}
	if resp.Balance <= 0 {
		return errors.New(errors.CodeForbidden, "余额不足，请充值")
	}
	return nil
}

func (h *LLMGatewayHandler) deductBalance(ctx context.Context, ownerID *string, cost float64) {
	if ownerID == nil || cost <= 0 {
		return
	}
	ctx = balanceContext(ctx, *ownerID)
	_, _ = h.svcCtx.UserClient.DeductBalance(ctx, &userpb.DeductBalanceReq{
		UserId: *ownerID,
		Amount: cost,
	})
}

// Balance RPCs operate on the charged owner, including trusted Bot calls
// without an end-user caller. Replace, rather than append, caller metadata.
func balanceContext(ctx context.Context, ownerID string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("user-id", ownerID)
	return metadata.NewOutgoingContext(ctx, md)
}

func (h *LLMGatewayHandler) calcCost(info *modelEntryInfo, inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1_000_000 * info.ModelEntry.InputPricePerMTok
	outputCost := float64(outputTokens) / 1_000_000 * info.ModelEntry.OutputPricePerMTok
	return inputCost + outputCost
}

// ----- Embed (component or provider-specific adapter, manual billing) -----

func (h *LLMGatewayHandler) Embed(ctx context.Context, req *pb.EmbedReq) (*pb.EmbedResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "embed")
	if err != nil {
		return nil, err
	}
	workload := "online"
	md, _ := metadata.FromIncomingContext(ctx)
	tokens := md.Get("x-aim-ingest-token")
	if len(tokens) > 0 {
		if len(tokens) != 1 || h.svcCtx.Config.IngestEmbeddingToken == "" || subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(h.svcCtx.Config.IngestEmbeddingToken)) != 1 {
			return nil, errors.ErrForbidden
		}
		workload = "ingest"
	}
	started := time.Now()
	result := "failed"
	defer func() {
		metrics.LLMEmbeddingRequests.Inc(workload, result)
		metrics.LLMEmbeddingDuration.Observe(time.Since(started).Seconds(), workload)
	}()

	modelName := info.ModelEntry.ModelName
	log.Infof("method=Embed model=%s model_id=%s input_count=%d bot_id=%s owner_id=%s", modelName, req.ModelId, len(req.Input), req.GetBotId(), req.GetOwnerId())

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}
	permit, err := h.acquireCapacity(ctx, info, req)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	ctx = permit.Context()

	apiKey := info.ModelEntry.APIKey
	baseURL := info.ModelEntry.BaseURL

	// Extract optional dimension from model metadata for models that support custom dimensions
	var dim int
	if d, ok := info.ModelEntry.Metadata["dimension"].(float64); ok {
		dim = int(d)
	}
	embeddings, promptTokens, err := llm.CreateEmbeddings(ctx, info.Provider, apiKey, baseURL, modelName, req.Input, dim)
	callContextErr := ctx.Err()
	finishCapacityAttempt(permit, err)
	if callContextErr != nil {
		return nil, status.FromContextError(callContextErr).Err()
	}
	if err != nil {
		log.Errorf("method=Embed model=%s embeddings failed", modelName)
		var invalid *llm.RequestError
		if stderrors.As(err, &invalid) {
			return nil, errors.New(errors.CodeInvalidParam, "invalid embedding input")
		}
		var rejection *llm.HTTPError
		if stderrors.As(err, &rejection) && (rejection.StatusCode == http.StatusBadRequest || rejection.StatusCode == http.StatusUnprocessableEntity) {
			return nil, errors.New(errors.CodeInvalidParam, "embedding input rejected")
		}
		return nil, errors.New(errors.CodeRPCError, "embeddings failed")
	}

	if promptTokens != nil && *promptTokens > 0 {
		h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "embed",
			&domain.UsageInfo{PromptTokens: *promptTokens, TotalTokens: *promptTokens})
		if info.TrackBilling {
			cost := h.calcCost(info, *promptTokens, 0)
			h.deductBalance(context.WithoutCancel(ctx), req.OwnerId, cost)
		}
	}

	data := make([]*pb.EmbedResp_Embedding, len(embeddings))
	for i, emb := range embeddings {
		data[i] = &pb.EmbedResp_Embedding{Index: int32(i), Embedding: emb}
	}
	resp := &pb.EmbedResp{Model: modelName, Data: data}
	if promptTokens != nil {
		resp.Usage = &pb.UsageInfo{PromptTokens: int32(*promptTokens), TotalTokens: int32(*promptTokens)}
	}
	if promptTokens != nil {
		log.Infof("method=Embed model=%s input_tokens=%d embeddings=%d", modelName, *promptTokens, len(embeddings))
	} else {
		log.Infof("method=Embed model=%s embeddings=%d", modelName, len(embeddings))
	}
	result = "success"
	return resp, nil
}

// ----- Rerank (direct HTTP, manual billing) -----

func (h *LLMGatewayHandler) Rerank(ctx context.Context, req *pb.RerankReq) (*pb.RerankResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "rerank")
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=Rerank model=%s model_id=%s docs=%d", modelName, req.ModelId, len(req.Documents))

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	permit, err := h.acquireCapacity(ctx, info, req)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	ctx = permit.Context()

	result, err := llm.Rerank(ctx, info.Provider, info.ModelEntry.APIKey, info.ModelEntry.BaseURL, modelName,
		req.Query, req.Documents, int(req.TopN), req.ReturnDocuments)
	callContextErr := ctx.Err()
	finishCapacityAttempt(permit, err)
	if callContextErr != nil {
		return nil, status.FromContextError(callContextErr).Err()
	}
	if err != nil {
		log.Errorf("method=Rerank model=%s error=%v", modelName, err)
		return nil, errors.Wrap(errors.CodeRPCError, "rerank failed", err)
	}

	if result.Tokens > 0 {
		h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "rerank",
			&domain.UsageInfo{PromptTokens: result.Tokens, TotalTokens: result.Tokens})
		if info.TrackBilling {
			cost := h.calcCost(info, result.Tokens, 0)
			h.deductBalance(context.WithoutCancel(ctx), req.OwnerId, cost)
		}
	}

	results := make([]*pb.RerankResp_Result, len(result.Items))
	for i, r := range result.Items {
		results[i] = &pb.RerankResp_Result{
			Index:          int32(r.Index),
			RelevanceScore: float32(r.RelevanceScore),
			Document:       r.Document,
		}
	}
	log.Infof("method=Rerank model=%s total_tokens=%d results=%d", modelName, result.Tokens, len(result.Items))
	return &pb.RerankResp{
		Results: results,
		Model:   modelName,
		Usage:   &pb.UsageInfo{TotalTokens: int32(result.Tokens)},
	}, nil
}

// ----- model resolution -----

type modelEntryInfo struct {
	*domain.ModelEntry
	Provider     string
	TrackBilling bool
}

func (h *LLMGatewayHandler) GetBillingStats(ctx context.Context, req *pb.BillingStatsReq) (*pb.BillingStatsResp, error) {
	if err := requireOwner(ctx, req.OwnerId); err != nil {
		return nil, err
	}
	if req.BotId != nil && identity.Validate(*req.BotId) != nil {
		return nil, errors.ErrInvalidParam
	}
	query := h.svcCtx.DB.WithContext(ctx).Model(&appModel.BillingRecord{}).Where("owner_id = ?", req.OwnerId)
	if req.BotId != nil {
		query = query.Where("bot_id = ?", *req.BotId)
	}
	var stats []struct {
		ModelName    string  `gorm:"column:model_name"`
		Capability   string  `gorm:"column:capability"`
		InputTokens  int64   `gorm:"column:input_tokens"`
		OutputTokens int64   `gorm:"column:output_tokens"`
		TotalCost    float64 `gorm:"column:total_cost"`
	}
	if err := query.Select(
		"model_name, capability, sum(input_tokens) as input_tokens, sum(output_tokens) as output_tokens, sum(input_cost + output_cost) as total_cost",
	).Group("model_name, capability").Find(&stats).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "billing stats query failed", err)
	}
	var totalInput, totalOutput int64
	var totalCost float64
	byModel := make([]*pb.BillingModelStat, 0, len(stats))
	for _, s := range stats {
		totalInput += s.InputTokens
		totalOutput += s.OutputTokens
		totalCost += s.TotalCost
		byModel = append(byModel, &pb.BillingModelStat{
			ModelName:    s.ModelName,
			Capability:   s.Capability,
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
			TotalCost:    s.TotalCost,
		})
	}
	return &pb.BillingStatsResp{
		TotalInputTokens:  totalInput,
		TotalOutputTokens: totalOutput,
		TotalCost:         totalCost,
		ByModel:           byModel,
	}, nil
}

func (h *LLMGatewayHandler) ListBillingRecords(ctx context.Context, req *pb.ListBillingRecordsReq) (*pb.ListBillingRecordsResp, error) {
	if err := requireOwner(ctx, req.OwnerId); err != nil {
		return nil, err
	}
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int64
	query := h.svcCtx.DB.WithContext(ctx).Model(&appModel.BillingRecord{}).Where("owner_id = ?", req.GetOwnerId())
	if err := query.Count(&total).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "billing count failed", err)
	}

	var records []appModel.BillingRecord
	if err := query.Order("created_at desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "billing records query failed", err)
	}

	items := make([]*pb.BillingRecordItem, 0, len(records))
	for _, r := range records {
		var inputTokens, outputTokens *int32
		if r.InputTokens != nil {
			value := int32(*r.InputTokens)
			inputTokens = &value
		}
		if r.OutputTokens != nil {
			value := int32(*r.OutputTokens)
			outputTokens = &value
		}
		items = append(items, &pb.BillingRecordItem{
			Id:           r.ID,
			BotId:        r.BotID,
			ModelId:      r.ModelID,
			OwnerId:      r.OwnerID,
			OwnerType:    r.OwnerType,
			ModelName:    r.ModelName,
			Capability:   r.Capability,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			InputCost:    r.InputCost,
			OutputCost:   r.OutputCost,
			TotalCost:    r.InputCost + r.OutputCost,
			Provider:     r.Provider,
			CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		})
	}

	return &pb.ListBillingRecordsResp{
		Items: items,
		Total: int32(total),
	}, nil
}

func (h *LLMGatewayHandler) recordBilling(entry *domain.ModelEntry, botID, ownerID *string, capability string, usage *domain.UsageInfo) {
	record, err := domain.NewBillingRecord(entry, botID, ownerID, capability, usage)
	if err == nil {
		err = h.svcCtx.BillingRepo.Record(record)
	}
	if err != nil {
		h.svcCtx.Logger.Errorf("record billing failed: model=%s capability=%s error=%v", entry.ID, capability, err)
	}
}

func (h *LLMGatewayHandler) VlmChat(ctx context.Context, req *pb.VlmChatReq) (*pb.ChatResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "vlm")
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=VlmChat model=%s model_id=%s owner_id=%s", modelName, req.ModelId, req.GetOwnerId())

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	permit, err := h.acquireCapacity(ctx, info, req)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	ctx = permit.Context()

	resp, err := llm.CompleteVLMChat(ctx, req, info.ModelEntry)
	callContextErr := ctx.Err()
	finishCapacityAttempt(permit, err)
	if callContextErr != nil {
		return nil, status.FromContextError(callContextErr).Err()
	}
	if err != nil {
		log.Errorf("method=VlmChat model=%s vlm chat failed", modelName)
		return nil, errors.New(errors.CodeRPCError, "vlm chat failed")
	}
	if resp.Usage != nil {
		log.Infof("method=VlmChat model=%s input_tokens=%d output_tokens=%d total_tokens=%d",
			modelName, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}

	// Persist usage for private and platform models; charge only platform calls.
	if resp.Usage != nil && resp.Usage.TotalTokens > 0 {
		h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "vlm",
			&domain.UsageInfo{
				PromptTokens:     int(resp.Usage.PromptTokens),
				CompletionTokens: int(resp.Usage.CompletionTokens),
				TotalTokens:      int(resp.Usage.TotalTokens),
			})
		if info.TrackBilling {
			cost := h.calcCost(info, int(resp.Usage.PromptTokens), int(resp.Usage.CompletionTokens))
			h.deductBalance(context.WithoutCancel(ctx), req.OwnerId, cost)
		}
	}

	return resp, nil
}
