package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	"github.com/maomeng/aim/app/llm-gateway/internal/metrics"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/interceptor"
)

// ResolveExecutionModel freezes routing, not permission or credentials.
func (h *LLMGatewayHandler) ResolveExecutionModel(ctx context.Context, req *pb.ResolveExecutionModelReq) (*pb.ModelExecutionConfig, error) {
	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "chat")
	if err != nil {
		return nil, err
	}
	baseURL, err := llm.ChatBaseURL(info.Provider, info.ModelEntry.BaseURL)
	if err != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid model routing")
	}
	cfg := &pb.ModelExecutionConfig{ModelId: req.ModelId, ModelName: info.ModelEntry.ModelName, Provider: info.Provider, BaseUrl: baseURL}
	cfg.Version = h.executionVersion(cfg, req.BotId, req.OwnerId)
	return cfg, nil
}

func (h *LLMGatewayHandler) executionVersion(cfg *pb.ModelExecutionConfig, botID, ownerID *string) string {
	encoded, _ := json.Marshal([]any{llm.AdapterVersion, cfg.ModelId, cfg.ModelName, cfg.Provider, cfg.BaseUrl, botID, ownerID})
	mac := hmac.New(sha256.New, h.svcCtx.EncKey)
	mac.Write(encoded)
	return llm.AdapterVersion + ":" + hex.EncodeToString(mac.Sum(nil))
}

func (h *LLMGatewayHandler) resolveChat(ctx context.Context, req *pb.ChatReq) (*modelEntryInfo, *pb.ModelExecutionConfig, error) {
	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "chat")
	if err != nil {
		return nil, nil, err
	}
	cfg := req.ExecutionModel
	if cfg == nil {
		baseURL, err := llm.ChatBaseURL(info.Provider, info.ModelEntry.BaseURL)
		if err != nil {
			return nil, nil, errors.New(errors.CodeInvalidParam, "invalid model routing")
		}
		cfg = &pb.ModelExecutionConfig{ModelId: req.ModelId, ModelName: info.ModelEntry.ModelName, Provider: info.Provider, BaseUrl: baseURL}
		cfg.Version = h.executionVersion(cfg, req.BotId, req.OwnerId)
	} else if cfg.ModelId != req.ModelId || cfg.ModelName == "" || !hmac.Equal([]byte(cfg.Version), []byte(h.executionVersion(cfg, req.BotId, req.OwnerId))) {
		return nil, nil, errors.New(errors.CodeInvalidParam, "invalid execution model snapshot")
	}
	// Credentials belong to the currently authorized endpoint. A routing change
	// invalidates this snapshot rather than sending a replacement endpoint's key
	// to the old endpoint; ordinary model-name changes still keep the frozen name.
	currentURL, err := llm.ChatBaseURL(info.Provider, info.ModelEntry.BaseURL)
	if err != nil || info.Provider != cfg.Provider || currentURL != cfg.BaseUrl {
		return nil, nil, errors.New(errors.CodeConflict, "model credential routing changed; start a new Run")
	}
	// Billing uses the executed route; eligibility and the credential are current.
	entry := *info.ModelEntry
	entry.ModelName, entry.Provider, entry.BaseURL = cfg.ModelName, cfg.Provider, cfg.BaseUrl
	info = &modelEntryInfo{ModelEntry: &entry, Provider: cfg.Provider, TrackBilling: info.TrackBilling}
	return info, cfg, nil
}

func (h *LLMGatewayHandler) recordChatUsage(ctx context.Context, info *modelEntryInfo, req *pb.ChatReq, usage *pb.UsageInfo) {
	if usage == nil {
		return
	}
	h.recordBilling(info.ModelEntry, req.BotId, req.OwnerId, "chat", &domain.UsageInfo{PromptTokens: int(usage.PromptTokens), CompletionTokens: int(usage.CompletionTokens), TotalTokens: int(usage.TotalTokens)})
	if info.TrackBilling {
		h.deductBalance(ctx, req.OwnerId, h.calcCost(info, int(usage.PromptTokens), int(usage.CompletionTokens)))
	}
}

func (h *LLMGatewayHandler) Chat(ctx context.Context, req *pb.ChatReq) (resp *pb.ChatResp, err error) {
	start := time.Now()
	info, cfg, err := h.resolveChat(ctx, req)
	if err != nil {
		return nil, err
	}
	modelName := cfg.ModelName
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		metrics.LLMRequestsTotal.Inc(modelName, result)
		metrics.LLMRequestDuration.Observe(time.Since(start).Seconds(), modelName, "false")
		if resp != nil && resp.Usage != nil {
			metrics.LLMPromptTokensTotal.Add(float64(resp.Usage.PromptTokens), modelName)
			metrics.LLMCompletionTokensTotal.Add(float64(resp.Usage.CompletionTokens), modelName)
		}
	}()
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
	defer h.svcCtx.RateLimiter.DecrConcurrency(context.WithoutCancel(ctx), modelName)
	resp, err = llm.CompleteChat(ctx, req, cfg, info.ModelEntry.APIKey)
	if err != nil {
		var input *llm.RequestError
		if stderrors.As(err, &input) {
			return nil, errors.New(errors.CodeInvalidParam, input.Error())
		}
		return nil, errors.Wrap(errors.CodeRPCError, "chat completion failed", err)
	}
	h.recordChatUsage(ctx, info, req, resp.Usage)
	h.svcCtx.Logger.WithContext(ctx).Infof("method=Chat model=%s adapter=%s usage_reported=%t", modelName, llm.AdapterVersion, resp.Usage != nil)
	return resp, nil
}

func (h *LLMGatewayHandler) ChatStream(req *pb.ChatReq, stream pb.LLMGateway_ChatStreamServer) (err error) {
	ctx, err := interceptor.WithUserIDFromMetadata(stream.Context())
	if err != nil {
		return err
	}
	ctx = interceptor.WithRequestIDFromMetadata(ctx)
	start := time.Now()
	info, cfg, err := h.resolveChat(ctx, req)
	if err != nil {
		return err
	}
	modelName := cfg.ModelName
	var usage *pb.UsageInfo
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		metrics.LLMRequestsTotal.Inc(modelName, result)
		metrics.LLMRequestDuration.Observe(time.Since(start).Seconds(), modelName, "true")
		if usage != nil {
			metrics.LLMPromptTokensTotal.Add(float64(usage.PromptTokens), modelName)
			metrics.LLMCompletionTokensTotal.Add(float64(usage.CompletionTokens), modelName)
			h.recordChatUsage(context.WithoutCancel(ctx), info, req, usage)
		}
	}()
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
	defer h.svcCtx.RateLimiter.DecrConcurrency(context.WithoutCancel(ctx), modelName)
	err = llm.StreamChat(ctx, req, cfg, info.ModelEntry.APIKey, func(chunk *pb.ChatStreamChunk) error {
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		return stream.Send(chunk)
	})
	if err != nil {
		var input *llm.RequestError
		if stderrors.As(err, &input) {
			return errors.New(errors.CodeInvalidParam, input.Error())
		}
		return errors.Wrap(errors.CodeRPCError, "stream chat failed", err)
	}
	h.svcCtx.Logger.WithContext(ctx).Infof("method=ChatStream model=%s adapter=%s usage_reported=%t", modelName, llm.AdapterVersion, usage != nil)
	return nil
}
