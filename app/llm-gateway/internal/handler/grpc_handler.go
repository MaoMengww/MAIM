package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	"github.com/maomeng/aim/app/llm-gateway/internal/metrics"
	appModel "github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/app/llm-gateway/internal/svc"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/maomeng/aim/pkg/errors"
)

type LLMGatewayHandler struct {
	pb.UnimplementedLLMGatewayServer
	svcCtx         *svc.ServiceContext
	billingHandler callbacks.Handler
}

func NewLLMGatewayHandler(svcCtx *svc.ServiceContext) *LLMGatewayHandler {
	return &LLMGatewayHandler{
		svcCtx:         svcCtx,
		billingHandler: llm.NewBillingCallbackHandler(svcCtx.BillingRepo),
	}
}

// resolveModel looks up a model by its registry ID and returns its info.
func (h *LLMGatewayHandler) resolveModel(modelID int64) (*modelEntryInfo, error) {
	if modelID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "model_id is required")
	}
	entry, err := h.svcCtx.ModelRepo.FindByID(modelID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "find model by id failed", err)
	}
	if entry == nil {
		return nil, status.Error(codes.NotFound, "model not found: id="+fmt.Sprint(modelID))
	}
	if entry.Status != "active" {
		return nil, status.Error(codes.PermissionDenied, "model disabled: "+entry.ModelName)
	}
	return &modelEntryInfo{ModelEntry: entry, Provider: entry.Provider, TrackBilling: entry.OwnerID == 0}, nil
}

// ----- Balance check for official models -----

func (h *LLMGatewayHandler) checkBalance(ctx context.Context, ownerID int64) error {
	if ownerID <= 0 {
		return nil
	}
	resp, err := h.svcCtx.UserClient.GetBalance(ctx, &userpb.GetBalanceReq{UserId: ownerID})
	if err != nil {
		return err
	}
	if resp.Balance <= 0 {
		return status.Error(codes.PermissionDenied, "余额不足，请充值")
	}
	return nil
}

func (h *LLMGatewayHandler) deductBalance(ctx context.Context, ownerID int64, cost float64) {
	if ownerID <= 0 || cost <= 0 {
		return
	}
	_, _ = h.svcCtx.UserClient.DeductBalance(ctx, &userpb.DeductBalanceReq{
		UserId: ownerID,
		Amount: cost,
	})
}

func (h *LLMGatewayHandler) calcCost(info *modelEntryInfo, inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1_000_000 * info.ModelEntry.InputPricePerMTok
	outputCost := float64(outputTokens) / 1_000_000 * info.ModelEntry.OutputPricePerMTok
	return inputCost + outputCost
}

// ----- Chat (Eino ChatModel + CallbackBilling) -----

func (h *LLMGatewayHandler) Chat(ctx context.Context, req *pb.ChatReq) (resp *pb.ChatResp, err error) {
	start := time.Now()
	log := h.svcCtx.Logger.WithContext(ctx)

	var info *modelEntryInfo
	defer func() {
		duration := time.Since(start).Seconds()
		status := "success"
		if err != nil {
			status = "error"
		}
		modelName := "unknown"
		if info != nil && info.ModelEntry != nil {
			modelName = info.ModelEntry.ModelName
		}
		metrics.LLMRequestsTotal.Inc(modelName, status)
		metrics.LLMRequestDuration.Observe(duration, modelName, "false")
		if resp != nil && resp.Usage != nil {
			metrics.LLMPromptTokensTotal.Add(float64(resp.Usage.PromptTokens), modelName)
			metrics.LLMCompletionTokensTotal.Add(float64(resp.Usage.CompletionTokens), modelName)
		}
	}()

	info, err = h.resolveModel(req.ModelId)
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=Chat model=%s model_id=%d bot_id=%d owner_id=%d", modelName, req.ModelId, req.BotId, req.OwnerId)

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	if err := h.svcCtx.RateLimiter.Allow(ctx, modelName); err != nil {
		return nil, err
	}
	if err := h.svcCtx.RateLimiter.IncrConcurrency(ctx, modelName); err != nil {
		return nil, err
	}
	defer h.svcCtx.RateLimiter.DecrConcurrency(ctx, modelName)

	cm := llm.NewChatModel(modelName, info.Provider, info.ModelEntry.APIKey, info.ModelEntry.BaseURL)

	if info.TrackBilling {
		ctx = llm.WithBillingInfo(ctx, &llm.BillingInfo{
			BotID:      req.BotId,
			OwnerID:    req.OwnerId,
			Capability: "chat",
			ModelEntry: info.ModelEntry,
		})
	}

	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{
		Name:      modelName,
		Type:      info.Provider,
		Component: components.ComponentOfChatModel,
	}, h.billingHandler)

	var chatOpts []model.Option
	if req.Temperature > 0 {
		chatOpts = append(chatOpts, model.WithTemperature(float32(req.Temperature)))
	}
	if req.TopP > 0 {
		chatOpts = append(chatOpts, model.WithTopP(float32(req.TopP)))
	}
	if req.MaxTokens > 0 {
		chatOpts = append(chatOpts, model.WithMaxTokens(int(req.MaxTokens)))
	}
	if len(req.Stop) > 0 {
		chatOpts = append(chatOpts, model.WithStop(req.Stop))
	}
	if len(req.Tools) > 0 {
		tools := make([]*schema.ToolInfo, len(req.Tools))
		for i, t := range req.Tools {
			ti := &schema.ToolInfo{Name: t.Name, Desc: t.Description}
			if len(t.Parameters) > 0 {
				var s jsonschema.Schema
				if err := json.Unmarshal(t.Parameters, &s); err == nil {
					ti.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&s)
				}
			}
			tools[i] = ti
		}
		chatOpts = append(chatOpts, model.WithTools(tools))
	}
	result, err := cm.Generate(ctx, llm.ProtoToEinoMessages(req.Messages), chatOpts...)
	if err != nil {
		log.Errorf("method=Chat model=%s error=%v", modelName, err)
		return nil, errors.Wrap(errors.CodeRPCError, "chat completion failed", err)
	}

	resp = &pb.ChatResp{}
	finishReason := ""
	if result.ResponseMeta != nil {
		finishReason = result.ResponseMeta.FinishReason
	}
	if result.Content != "" || len(result.ToolCalls) > 0 {
		resp.Choices = []*pb.ChatResp_Choice{{
			Index:        0,
			Message:      llm.EinoMessageToProto(result),
			FinishReason: finishReason,
		}}
	}
	if result.ResponseMeta != nil && result.ResponseMeta.Usage != nil {
		resp.Usage = llm.SchemaTokenUsageToProto(result.ResponseMeta.Usage)
	}

	// Deduct balance for official models
	if info.TrackBilling && resp.Usage != nil && resp.Usage.TotalTokens > 0 {
		cost := h.calcCost(info, int(resp.Usage.PromptTokens), int(resp.Usage.CompletionTokens))
		h.deductBalance(ctx, req.OwnerId, cost)
	}

	if resp.Usage != nil {
		log.Infof("method=Chat model=%s input_tokens=%d output_tokens=%d total_tokens=%d", modelName, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	} else {
		log.Infof("method=Chat model=%s completed", modelName)
	}
	return resp, nil
}

// ----- ChatStream (Eino ChatModel + CallbackBilling) -----

func (h *LLMGatewayHandler) ChatStream(req *pb.ChatReq, stream pb.LLMGateway_ChatStreamServer) error {
	ctx := stream.Context()
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(req.ModelId)
	if err != nil {
		return err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=ChatStream model=%s model_id=%d bot_id=%d owner_id=%d", modelName, req.ModelId, req.BotId, req.OwnerId)

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return err
		}
	}

	if err := h.svcCtx.RateLimiter.Allow(ctx, modelName); err != nil {
		return err
	}
	if err := h.svcCtx.RateLimiter.IncrConcurrency(ctx, modelName); err != nil {
		return err
	}
	defer h.svcCtx.RateLimiter.DecrConcurrency(ctx, modelName)

	cm := llm.NewChatModel(modelName, info.Provider, info.ModelEntry.APIKey, info.ModelEntry.BaseURL)

	if info.TrackBilling {
		ctx = llm.WithBillingInfo(ctx, &llm.BillingInfo{
			BotID:      req.BotId,
			OwnerID:    req.OwnerId,
			Capability: "chat",
			ModelEntry: info.ModelEntry,
		})
	}

	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{
		Name:      modelName,
		Type:      info.Provider,
		Component: components.ComponentOfChatModel,
	}, h.billingHandler)

	var chatOpts []model.Option
	if req.Temperature > 0 {
		chatOpts = append(chatOpts, model.WithTemperature(float32(req.Temperature)))
	}
	if req.TopP > 0 {
		chatOpts = append(chatOpts, model.WithTopP(float32(req.TopP)))
	}
	if req.MaxTokens > 0 {
		chatOpts = append(chatOpts, model.WithMaxTokens(int(req.MaxTokens)))
	}
	if len(req.Stop) > 0 {
		chatOpts = append(chatOpts, model.WithStop(req.Stop))
	}
	if len(req.Tools) > 0 {
		tools := make([]*schema.ToolInfo, len(req.Tools))
		for i, t := range req.Tools {
			ti := &schema.ToolInfo{Name: t.Name, Desc: t.Description}
			if len(t.Parameters) > 0 {
				var s jsonschema.Schema
				if err := json.Unmarshal(t.Parameters, &s); err == nil {
					ti.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&s)
				}
			}
			tools[i] = ti
		}
		chatOpts = append(chatOpts, model.WithTools(tools))
	}
	sr, err := cm.Stream(ctx, llm.ProtoToEinoMessages(req.Messages), chatOpts...)
	if err != nil {
		log.Errorf("method=ChatStream model=%s error=%v", modelName, err)
		return errors.Wrap(errors.CodeRPCError, "stream chat failed", err)
	}
	defer sr.Close()

	var lastUsage *pb.UsageInfo
	for {
		chunk, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Errorf("method=ChatStream model=%s recv error=%v", modelName, err)
			return errors.Wrap(errors.CodeRPCError, "stream recv failed", err)
		}
		finishReason := ""
		if chunk.ResponseMeta != nil {
			finishReason = chunk.ResponseMeta.FinishReason
			if chunk.ResponseMeta.Usage != nil {
				lastUsage = llm.SchemaTokenUsageToProto(chunk.ResponseMeta.Usage)
			}
		}
		if err := stream.Send(&pb.ChatStreamChunk{
			Choices: []*pb.ChatStreamChunk_Choice{{
				Index:        0,
				Delta:        llm.EinoMessageToProto(chunk),
				FinishReason: finishReason,
			}},
		}); err != nil {
			return err
		}
	}

	// Deduct balance for official models
	if info.TrackBilling && lastUsage != nil && lastUsage.TotalTokens > 0 {
		cost := h.calcCost(info, int(lastUsage.PromptTokens), int(lastUsage.CompletionTokens))
		h.deductBalance(ctx, req.OwnerId, cost)
	}

	log.Infof("method=ChatStream model=%s completed", modelName)
	return nil
}

// ----- Embed (direct HTTP, manual billing) -----

func (h *LLMGatewayHandler) Embed(ctx context.Context, req *pb.EmbedReq) (*pb.EmbedResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(req.ModelId)
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=Embed model=%s model_id=%d input_count=%d bot_id=%d owner_id=%d", modelName, req.ModelId, len(req.Input), req.BotId, req.OwnerId)

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	apiKey := info.ModelEntry.APIKey
	baseURL := info.ModelEntry.BaseURL

	// Extract optional dimension from model metadata for models that support custom dimensions
	var dim int
	if d, ok := info.ModelEntry.Metadata["dimension"].(float64); ok {
		dim = int(d)
	}
	embeddings, promptTokens, err := llm.CreateEmbeddings(ctx, apiKey, baseURL, modelName, req.Input, dim)
	if err != nil {
		log.Errorf("method=Embed model=%s error=%v", modelName, err)
		return nil, errors.Wrap(errors.CodeRPCError, "embeddings failed: "+err.Error(), err)
	}

	if promptTokens != nil && *promptTokens > 0 && info.TrackBilling && info.ModelEntry != nil {
		h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "embed",
			&domain.UsageInfo{PromptTokens: *promptTokens, TotalTokens: *promptTokens})
		cost := h.calcCost(info, *promptTokens, 0)
		h.deductBalance(ctx, req.OwnerId, cost)
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
	return resp, nil
}

// ----- Rerank (direct HTTP, manual billing) -----

func (h *LLMGatewayHandler) Rerank(ctx context.Context, req *pb.RerankReq) (*pb.RerankResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(req.ModelId)
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	queryTrunc := req.Query
	if len(queryTrunc) > 200 {
		queryTrunc = queryTrunc[:200] + "..."
	}
	log.Infof("method=Rerank model=%s model_id=%d query=%s docs=%d", modelName, req.ModelId, queryTrunc, len(req.Documents))

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	if err := h.svcCtx.RateLimiter.Allow(ctx, modelName); err != nil {
		return nil, err
	}
	if err := h.svcCtx.RateLimiter.IncrConcurrency(ctx, modelName); err != nil {
		return nil, err
	}
	defer h.svcCtx.RateLimiter.DecrConcurrency(ctx, modelName)

	result, err := llm.Rerank(ctx, info.ModelEntry.APIKey, info.ModelEntry.BaseURL, modelName,
		req.Query, req.Documents, int(req.TopN), req.ReturnDocuments)
	if err != nil {
		log.Errorf("method=Rerank model=%s error=%v", modelName, err)
		return nil, errors.Wrap(errors.CodeRPCError, "rerank failed", err)
	}

	if result.Tokens > 0 && info.TrackBilling && info.ModelEntry != nil {
		h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "rerank",
			&domain.UsageInfo{TotalTokens: result.Tokens})
		cost := h.calcCost(info, result.Tokens, 0)
		h.deductBalance(ctx, req.OwnerId, cost)
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

// ----- Model CRUD -----

func (h *LLMGatewayHandler) ListModels(ctx context.Context, req *pb.ListModelsReq) (*pb.ListModelsResp, error) {
	entries := h.svcCtx.ModelRepo.ListAll()
	var items []*pb.ModelResp
	for _, e := range entries {
		if e.Status != "active" {
			continue
		}
		if req.Capability != "" && e.Capability != req.Capability {
			continue
		}
		if e.OwnerID != 0 && e.OwnerID != req.OwnerId {
			continue
		}
		items = append(items, &pb.ModelResp{
			Id:                 e.ID,
			ModelName:          e.ModelName,
			Provider:           e.Provider,
			Capability:         e.Capability,
			BaseUrl:            e.BaseURL,
			OwnerId:            e.OwnerID,
			Status:             e.Status,
			InputPricePerMtok:  e.InputPricePerMTok,
			OutputPricePerMtok: e.OutputPricePerMTok,
			ApiKey:             e.APIKey,
		})
	}
	return &pb.ListModelsResp{Items: items, Total: int32(len(items))}, nil
}

func (h *LLMGatewayHandler) CreateModel(ctx context.Context, req *pb.CreateModelReq) (*pb.ModelResp, error) {
	encrypted, err := crypto.EncryptString(req.ApiKey, h.svcCtx.EncKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "encrypt api key failed")
	}
	recID, err := h.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("generate model id failed: %v", err))
	}
	rec := &appModel.ModelRegistry{
		ID:                 recID,
		ModelName:          req.ModelName,
		Provider:           req.Provider,
		Capability:         req.Capability,
		BaseURL:            req.BaseUrl,
		APIKeyEncrypted:    encrypted,
		InputPricePerMTok:  req.InputPricePerMtok,
		OutputPricePerMTok: req.OutputPricePerMtok,
		Status:             "active",
		OwnerID:            req.OwnerId,
	}
	if err := h.svcCtx.DB.WithContext(ctx).Create(rec).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "create model failed", err)
	}
	_ = h.svcCtx.ModelRepo.Refresh()
	return &pb.ModelResp{
		Id:                 rec.ID,
		ModelName:          rec.ModelName,
		Provider:           rec.Provider,
		Capability:         rec.Capability,
		BaseUrl:            rec.BaseURL,
		OwnerId:            rec.OwnerID,
		Status:             rec.Status,
		InputPricePerMtok:  rec.InputPricePerMTok,
		OutputPricePerMtok: rec.OutputPricePerMTok,
		ApiKey:             req.ApiKey,
	}, nil
}

func (h *LLMGatewayHandler) UpdateModel(ctx context.Context, req *pb.UpdateModelReq) (*pb.ModelResp, error) {
	var current appModel.ModelRegistry
	if err := h.svcCtx.DB.WithContext(ctx).First(&current, req.ModelId).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "model not found", err)
	}
	if current.OwnerID == 0 {
		return nil, status.Error(codes.PermissionDenied, "platform models cannot be modified")
	}
	updates := map[string]any{}
	if req.ModelName != "" {
		updates["model_name"] = req.ModelName
	}
	if req.Provider != "" {
		updates["provider"] = req.Provider
	}
	if req.Capability != "" {
		updates["capability"] = req.Capability
	}
	if req.BaseUrl != "" {
		updates["base_url"] = req.BaseUrl
	}
	if req.ApiKey != "" {
		encrypted, err := crypto.EncryptString(req.ApiKey, h.svcCtx.EncKey)
		if err != nil {
			return nil, status.Error(codes.Internal, "encrypt api key failed")
		}
		updates["api_key_encrypted"] = encrypted
	}
	if req.InputPricePerMtok > 0 {
		updates["input_price_per_mtok"] = req.InputPricePerMtok
	}
	if req.OutputPricePerMtok > 0 {
		updates["output_price_per_mtok"] = req.OutputPricePerMtok
	}
	if err := h.svcCtx.DB.WithContext(ctx).Model(&appModel.ModelRegistry{}).Where("id = ?", req.ModelId).Updates(updates).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update model failed", err)
	}
	_ = h.svcCtx.ModelRepo.Refresh()
	entry, err := h.svcCtx.ModelRepo.FindByID(req.ModelId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "find model after update failed", err)
	}
	if entry == nil {
		return nil, status.Error(codes.NotFound, "model not found after update")
	}
	return &pb.ModelResp{
		Id:                 entry.ID,
		ModelName:          entry.ModelName,
		Provider:           entry.Provider,
		Capability:         entry.Capability,
		BaseUrl:            entry.BaseURL,
		OwnerId:            entry.OwnerID,
		Status:             entry.Status,
		InputPricePerMtok:  entry.InputPricePerMTok,
		OutputPricePerMtok: entry.OutputPricePerMTok,
		ApiKey:             entry.APIKey,
	}, nil
}

func (h *LLMGatewayHandler) DeleteModel(ctx context.Context, req *pb.DeleteModelReq) (*pb.DeleteModelResp, error) {
	var current appModel.ModelRegistry
	if err := h.svcCtx.DB.WithContext(ctx).First(&current, req.ModelId).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "model not found", err)
	}
	if current.OwnerID == 0 {
		return nil, status.Error(codes.PermissionDenied, "platform models cannot be deleted")
	}
	if err := h.svcCtx.DB.WithContext(ctx).Model(&appModel.ModelRegistry{}).Where("id = ?", req.ModelId).Update("status", "disabled").Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "delete model failed", err)
	}
	_ = h.svcCtx.ModelRepo.Refresh()
	return &pb.DeleteModelResp{Success: true}, nil
}

func (h *LLMGatewayHandler) GetBillingStats(ctx context.Context, req *pb.BillingStatsReq) (*pb.BillingStatsResp, error) {
	query := h.svcCtx.DB.WithContext(ctx).Model(&appModel.BillingRecord{}).Where("owner_id = ?", req.OwnerId)
	if req.BotId > 0 {
		query = query.Where("bot_id = ?", req.BotId)
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
		items = append(items, &pb.BillingRecordItem{
			Id:           r.ID,
			BotId:        r.BotID,
			ModelName:    r.ModelName,
			Capability:   r.Capability,
			InputTokens:  int32(r.InputTokens),
			OutputTokens: int32(r.OutputTokens),
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

func (h *LLMGatewayHandler) recordBilling(entry *domain.ModelEntry, botID, ownerID int64, capability string, usage *domain.UsageInfo) {
	inputCost := float64(usage.PromptTokens) / 1_000_000 * entry.InputPricePerMTok
	outputCost := float64(usage.CompletionTokens) / 1_000_000 * entry.OutputPricePerMTok

	record := &appModel.BillingRecord{
		BotID:        botID,
		OwnerID:      ownerID,
		ModelName:    entry.ModelName,
		Capability:   capability,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		InputCost:    inputCost,
		OutputCost:   outputCost,
		Provider:     entry.Provider,
		CreatedAt:    time.Now(),
	}
	if err := h.svcCtx.BillingRepo.Record(record); err != nil {
		_ = err
	}
}

func (h *LLMGatewayHandler) VlmChat(ctx context.Context, req *pb.VlmChatReq) (*pb.ChatResp, error) {
	log := h.svcCtx.Logger.WithContext(ctx)

	info, err := h.resolveModel(req.ModelId)
	if err != nil {
		return nil, err
	}

	modelName := info.ModelEntry.ModelName
	log.Infof("method=VlmChat model=%s model_id=%d owner_id=%d", modelName, req.ModelId, req.OwnerId)

	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}

	if err := h.svcCtx.RateLimiter.Allow(ctx, modelName); err != nil {
		return nil, err
	}
	if err := h.svcCtx.RateLimiter.IncrConcurrency(ctx, modelName); err != nil {
		return nil, err
	}
	defer h.svcCtx.RateLimiter.DecrConcurrency(ctx, modelName)

	// Build VLM payload with image content
	baseURL := strings.TrimRight(info.ModelEntry.BaseURL, "/")
	messages := make([]map[string]any, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.SystemPrompt})
	}
	content := []map[string]any{
		{"type": "text", "text": req.UserPrompt},
		{"type": "image_url", "image_url": map[string]string{"url": req.ImageData}},
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})

	body := map[string]any{
		"model":    modelName,
		"messages": messages,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	respBytes, err := llm.DoRequest(ctx, baseURL+"/chat/completions", info.ModelEntry.APIKey, body)
	if err != nil {
		log.Errorf("method=VlmChat model=%s error=%v", modelName, err)
		return nil, errors.Wrap(errors.CodeRPCError, "vlm chat failed", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "decode vlm response", err)
	}

	resp := &pb.ChatResp{}
	if len(result.Choices) > 0 {
		c := result.Choices[0]
		resp.Choices = []*pb.ChatResp_Choice{{
			Index:        0,
			Message:      &pb.Message{Role: "assistant", Content: c.Message.Content},
			FinishReason: c.FinishReason,
		}}
	}
	if result.Usage != nil {
		resp.Usage = &pb.UsageInfo{
			PromptTokens:     int32(result.Usage.PromptTokens),
			CompletionTokens: int32(result.Usage.CompletionTokens),
			TotalTokens:      int32(result.Usage.TotalTokens),
		}
		log.Infof("method=VlmChat model=%s input_tokens=%d output_tokens=%d total_tokens=%d",
			modelName, result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	}

	// Record billing for official models
	if info.TrackBilling && result.Usage != nil && result.Usage.TotalTokens > 0 {
		h.recordBilling(info.ModelEntry, 0, req.OwnerId, "vlm",
			&domain.UsageInfo{
				PromptTokens:     result.Usage.PromptTokens,
				CompletionTokens: result.Usage.CompletionTokens,
				TotalTokens:      result.Usage.TotalTokens,
			})
		cost := h.calcCost(info, result.Usage.PromptTokens, result.Usage.CompletionTokens)
		h.deductBalance(ctx, req.OwnerId, cost)
	}

	return resp, nil
}
