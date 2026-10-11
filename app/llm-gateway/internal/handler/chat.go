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
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ResolveExecutionModel freezes routing and credential identity, never the secret or permission.
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
	cfg.Version = h.executionVersion(cfg, req.BotId, req.OwnerId, info.ModelEntry.APIKey)
	return cfg, nil
}

func (h *LLMGatewayHandler) executionVersion(cfg *pb.ModelExecutionConfig, botID, ownerID *string, apiKey string) string {
	encoded, _ := json.Marshal([]any{llm.AdapterVersion, cfg.ModelId, cfg.ModelName, cfg.Provider, cfg.BaseUrl, botID, ownerID, apiKey})
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
		cfg.Version = h.executionVersion(cfg, req.BotId, req.OwnerId, info.ModelEntry.APIKey)
	} else if cfg.ModelId != req.ModelId || cfg.ModelName == "" {
		return nil, nil, errors.New(errors.CodeInvalidParam, "invalid execution model snapshot")
	}
	// Authenticate the frozen fields and current credential before interpreting
	// route changes. Ordinary model-name changes keep the frozen name.
	if !hmac.Equal([]byte(cfg.Version), []byte(h.executionVersion(cfg, req.BotId, req.OwnerId, info.ModelEntry.APIKey))) {
		return nil, nil, errors.New(errors.CodeInvalidParam, "invalid execution model snapshot")
	}
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
	if err := h.authorizeRunRequest(ctx, req); err != nil {
		return nil, err
	}
	resp, err = llm.CompleteChat(ctx, req, cfg, info.ModelEntry.APIKey)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
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
	if err := h.authorizeRunRequest(ctx, req); err != nil {
		return err
	}
	err = llm.StreamChat(ctx, req, cfg, info.ModelEntry.APIKey, func(chunk *pb.ChatStreamChunk) error {
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		return stream.Send(chunk)
	})
	if err != nil {
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}
		var input *llm.RequestError
		if stderrors.As(err, &input) {
			return errors.New(errors.CodeInvalidParam, input.Error())
		}
		return errors.Wrap(errors.CodeRPCError, "stream chat failed", err)
	}
	h.svcCtx.Logger.WithContext(ctx).Infof("method=ChatStream model=%s adapter=%s usage_reported=%t", modelName, llm.AdapterVersion, usage != nil)
	return nil
}

// Resource permission is current platform state, independent of the frozen
// model route and provider cache. Non-Run model consumers retain their contract.
func (h *LLMGatewayHandler) authorizeRunRequest(ctx context.Context, req *pb.ChatReq) error {
	if req.RunId == nil {
		return nil
	}
	if identity.Validate(*req.RunId) != nil || req.BotId == nil || req.ExecutionGeneration < 1 || req.InputVersion < 1 {
		return status.Error(codes.InvalidArgument, "invalid Run inference authority")
	}
	return h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run struct {
			BotID, ConvID, BindingID, RequesterID, ControllerID, Visibility, Status                 string
			ControllerMemberID                                                                      *string
			ResourceGrantIDs                                                                        []string `gorm:"serializer:json"`
			AccessRevoked                                                                           bool
			ExecutionGeneration, InputVersion, BotAuthorizationVersion, BindingAuthorizationVersion int64
		}
		if err := tx.Table("bot.runs").Where("id = ? AND bot_id = ?", *req.RunId, *req.BotId).Take(&run).Error; err != nil {
			return status.Error(codes.PermissionDenied, "Run inference authorization unavailable")
		}
		if run.AccessRevoked || run.Status != "running" || run.ExecutionGeneration != req.ExecutionGeneration || req.InputVersion > run.InputVersion {
			return status.Error(codes.PermissionDenied, "Run inference authority changed")
		}
		var count int64
		// A frozen request and matching generation do not prove a live claim.
		// Check the same durable lease as the Bot execution boundary before any
		// provider dispatch; expired owners cannot spend another model call.
		if err := tx.Table("bot.runs AS r").Where("r.id = ? AND r.execution_generation = ?", *req.RunId, req.ExecutionGeneration).
			Where("(r.executor_type = 'third_party' AND r.executor_lease_until > NOW()) OR (r.executor_type = 'builtin' AND EXISTS (SELECT 1 FROM bot.run_outbox lease WHERE lease.run_id = r.id AND lease.kind = 'execute' AND lease.execution_generation = r.execution_generation AND lease.completed_at IS NULL AND lease.lease_until > NOW()))").Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return status.Error(codes.PermissionDenied, "Run inference claim expired or superseded")
		}
		if err := tx.Table("messaging.conv_bots AS c").Joins("JOIN bot.bots b ON b.id = c.bot_id").Where("c.id = ? AND c.bot_id = ? AND c.conv_id = ? AND c.revoked_at IS NULL AND c.authorization_version = ? AND b.status = 'active' AND b.authorization_version = ? AND COALESCE(NULLIF(c.bot_settings, '')::jsonb->>'enabled', 'true') = 'true'", run.BindingID, run.BotID, run.ConvID, run.BindingAuthorizationVersion, run.BotAuthorizationVersion).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 || run.ControllerMemberID == nil {
			return status.Error(codes.PermissionDenied, "Run participation revoked")
		}
		if err := tx.Table("messaging.conv_members").Where("id = ? AND conv_id = ? AND user_id = ? AND member_type = 'user'", *run.ControllerMemberID, run.ConvID, run.ControllerID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return status.Error(codes.PermissionDenied, "Run controller membership revoked")
		}
		access := resourcegrant.Access{BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID, Visibility: run.Visibility, GrantIDs: run.ResourceGrantIDs}
		if err := resourcegrant.CheckIDs(tx, access, run.Visibility != "personal", false); err != nil {
			return err
		}
		return resourcegrant.CheckRunToolPermissions(tx, access, *req.RunId, false)
	})
}
