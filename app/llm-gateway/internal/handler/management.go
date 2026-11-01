package handler

import (
	"context"
	stderrors "errors"
	"math"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	appModel "github.com/maomeng/aim/app/llm-gateway/internal/model"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func managementOwner(ctx context.Context) (string, error) {
	owner, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(owner) != nil {
		return "", errors.ErrUnauthorized
	}
	return owner, nil
}

func managementError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.IsBizError(err); ok {
		return err
	}
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if stderrors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23505") {
		return errors.New(errors.CodeConflict, "resource is referenced or conflicts with existing data")
	}
	return errors.Wrap(errors.CodeDBError, "model management operation failed", err)
}

func ownRecord(tx *gorm.DB, owner, id string, record any, lock bool) error {
	if identity.Validate(id) != nil {
		return errors.ErrInvalidParam
	}
	q := tx.Where("id = ? AND owner_type = ? AND owner_id = ?", id, "user", owner)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return managementError(q.First(record).Error)
}

func limitValues(limits *pb.CapacityLimits) (domain.CapacityLimits, error) {
	out := domain.CapacityLimits{RequestsPerMinute: int(limits.GetRequestsPerMinute()), RequestsPerSecond: int(limits.GetRequestsPerSecond()), TokensPerSecond: int(limits.GetTokensPerSecond()), Concurrency: int(limits.GetConcurrency())}
	if out.RequestsPerMinute < 0 || out.RequestsPerSecond < 0 || out.TokensPerSecond < 0 || out.Concurrency < 0 {
		return out, errors.New(errors.CodeInvalidParam, "capacity limits must be nonnegative")
	}
	return out, nil
}

func protoLimits(l domain.CapacityLimits) *pb.CapacityLimits {
	return &pb.CapacityLimits{RequestsPerMinute: int32(l.RequestsPerMinute), RequestsPerSecond: int32(l.RequestsPerSecond), TokensPerSecond: int32(l.TokensPerSecond), Concurrency: int32(l.Concurrency)}
}

func limitUpdates(l domain.CapacityLimits) map[string]any {
	return map[string]any{"requests_per_minute": l.RequestsPerMinute, "requests_per_second": l.RequestsPerSecond, "tokens_per_second": l.TokensPerSecond, "concurrency": l.Concurrency}
}

func modelResponse(e *domain.ModelEntry) *pb.ModelResp {
	return &pb.ModelResp{Id: e.ID, ModelName: e.ModelName, Provider: e.Provider, Capability: e.Capability, BaseUrl: e.BaseURL, OwnerId: e.OwnerID, OwnerType: e.OwnerType, Status: e.Status, InputPricePerMtok: e.InputPricePerMTok, OutputPricePerMtok: e.OutputPricePerMTok, ConnectionId: e.ConnectionID, QuotaPoolId: e.QuotaPoolID, PoolLimits: protoLimits(e.PoolLimits), ModelLimits: protoLimits(e.ModelLimits)}
}

func (h *LLMGatewayHandler) ListModels(ctx context.Context, req *pb.ListModelsReq) (*pb.ListModelsResp, error) {
	if err := validateInvocation(ctx, nil, req.OwnerId); err != nil {
		return nil, err
	}
	items := make([]*pb.ModelResp, 0)
	for _, e := range h.svcCtx.ModelRepo.ListAll() {
		if e.Status != "active" || (req.Capability != "" && e.Capability != req.Capability) || !modelVisible(e.OwnerType, e.OwnerID, req.OwnerId) {
			continue
		}
		items = append(items, modelResponse(e))
	}
	return &pb.ListModelsResp{Items: items, Total: int32(len(items))}, nil
}

func validateModel(capability, provider, name string, inputPrice, outputPrice float64) error {
	if strings.TrimSpace(name) == "" || len(name) > 256 {
		return errors.New(errors.CodeInvalidParam, "model name is required and must fit 256 bytes")
	}
	switch capability {
	case "chat", "embed", "rerank", "judge", "vlm", "tts":
	default:
		return errors.New(errors.CodeInvalidParam, "invalid model capability")
	}
	if math.IsNaN(inputPrice) || math.IsInf(inputPrice, 0) || inputPrice < 0 || math.IsNaN(outputPrice) || math.IsInf(outputPrice, 0) || outputPrice < 0 {
		return errors.ErrInvalidParam
	}
	return validateJudgmentModel(capability, provider, name, inputPrice, outputPrice)
}

func (h *LLMGatewayHandler) refreshedModel(ctx context.Context, id string) (*pb.ModelResp, error) {
	if err := h.svcCtx.ModelRepo.Refresh(); err != nil {
		return nil, managementError(err)
	}
	entry, err := h.svcCtx.ModelRepo.FindByID(ctx, id)
	if err != nil {
		return nil, managementError(err)
	}
	if entry == nil {
		return nil, errors.ErrNotFound
	}
	return modelResponse(entry), nil
}

func (h *LLMGatewayHandler) CreateModel(ctx context.Context, req *pb.CreateModelReq) (*pb.ModelResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	if req.OwnerType != "user" || req.OwnerId == nil || *req.OwnerId != owner {
		return nil, errors.ErrForbidden
	}
	limits, err := limitValues(req.ModelLimits)
	if err != nil {
		return nil, err
	}
	id, err := identity.New()
	if err != nil {
		return nil, errors.ErrInternal
	}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var connection appModel.ProviderConnection
		if err := ownRecord(tx, owner, req.ConnectionId, &connection, true); err != nil {
			return err
		}
		if err := validateModel(req.Capability, connection.Provider, req.ModelName, req.InputPricePerMtok, req.OutputPricePerMtok); err != nil {
			return err
		}
		return tx.Create(&appModel.ModelRegistry{ID: id, ModelName: req.ModelName, ConnectionID: connection.ID, Capability: req.Capability, InputPricePerMTok: req.InputPricePerMtok, OutputPricePerMTok: req.OutputPricePerMtok, OwnerType: "user", OwnerID: &owner, Status: "active", RequestsPerMinute: limits.RequestsPerMinute, RequestsPerSecond: limits.RequestsPerSecond, TokensPerSecond: limits.TokensPerSecond, Concurrency: limits.Concurrency}).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	return h.refreshedModel(ctx, id)
}

func (h *LLMGatewayHandler) UpdateModel(ctx context.Context, req *pb.UpdateModelReq) (*pb.ModelResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := limitValues(req.ModelLimits)
	if err != nil {
		return nil, err
	}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current appModel.ModelRegistry
		if err := ownRecord(tx, owner, req.ModelId, &current, true); err != nil {
			return err
		}
		connectionID := current.ConnectionID
		if req.ConnectionId != nil {
			connectionID = *req.ConnectionId
		}
		var connection appModel.ProviderConnection
		if err := ownRecord(tx, owner, connectionID, &connection, true); err != nil {
			return err
		}
		name, capability := current.ModelName, current.Capability
		if req.ModelName != "" {
			name = req.ModelName
		}
		if req.Capability != "" {
			capability = req.Capability
		}
		inputPrice, outputPrice := current.InputPricePerMTok, current.OutputPricePerMTok
		if req.InputPricePerMtok != nil {
			inputPrice = *req.InputPricePerMtok
		}
		if req.OutputPricePerMtok != nil {
			outputPrice = *req.OutputPricePerMtok
		}
		if err := validateModel(capability, connection.Provider, name, inputPrice, outputPrice); err != nil {
			return err
		}
		updates := map[string]any{"model_name": name, "capability": capability, "connection_id": connectionID, "input_price_per_mtok": inputPrice, "output_price_per_mtok": outputPrice}
		if req.ModelLimits != nil {
			for k, v := range limitUpdates(limits) {
				updates[k] = v
			}
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	return h.refreshedModel(ctx, req.ModelId)
}

func (h *LLMGatewayHandler) DeleteModel(ctx context.Context, req *pb.DeleteModelReq) (*pb.DeleteModelResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current appModel.ModelRegistry
		if err := ownRecord(tx, owner, req.ModelId, &current, true); err != nil {
			return err
		}
		return tx.Model(&current).Update("status", "disabled").Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	if err := h.svcCtx.ModelRepo.Refresh(); err != nil {
		return nil, managementError(err)
	}
	return &pb.DeleteModelResp{Success: true}, nil
}

func validateConnection(name, provider, baseURL, key string) error {
	if strings.TrimSpace(name) == "" || len(name) > 256 || strings.TrimSpace(provider) == "" || len(provider) > 64 || len(baseURL) > 4096 || len(key) > 65536 {
		return errors.ErrInvalidParam
	}
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return errors.New(errors.CodeInvalidParam, "invalid provider base URL")
		}
	}
	return nil
}

func connectionResponse(c appModel.ProviderConnection) *pb.ConnectionResp {
	return &pb.ConnectionResp{Id: c.ID, Name: c.Name, Provider: c.Provider, BaseUrl: c.BaseURL, QuotaPoolId: c.QuotaPoolID, OwnerId: c.OwnerID, OwnerType: c.OwnerType}
}

func poolResponse(p appModel.QuotaPool) *pb.QuotaPoolResp {
	return &pb.QuotaPoolResp{Id: p.ID, Name: p.Name, OwnerId: p.OwnerID, OwnerType: p.OwnerType, Limits: protoLimits(domain.CapacityLimits{RequestsPerMinute: p.RequestsPerMinute, RequestsPerSecond: p.RequestsPerSecond, TokensPerSecond: p.TokensPerSecond, Concurrency: p.Concurrency})}
}

func managementPage(page, pageSize int32) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 100
	}
	return int(page-1) * int(pageSize), int(pageSize)
}

func (h *LLMGatewayHandler) ListConnections(ctx context.Context, req *pb.ListConnectionsReq) (*pb.ListConnectionsResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	query := h.svcCtx.DB.WithContext(ctx).Model(&appModel.ProviderConnection{}).Where("owner_type = ? AND owner_id = ?", "user", owner)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, managementError(err)
	}
	offset, size := managementPage(req.Page, req.PageSize)
	var records []appModel.ProviderConnection
	if err := query.Select("id", "name", "provider", "base_url", "quota_pool_id", "owner_id", "owner_type").Order("created_at, id").Offset(offset).Limit(size).Find(&records).Error; err != nil {
		return nil, managementError(err)
	}
	items := make([]*pb.ConnectionResp, 0, len(records))
	for _, c := range records {
		items = append(items, connectionResponse(c))
	}
	return &pb.ListConnectionsResp{Items: items, Total: int32(total)}, nil
}

func (h *LLMGatewayHandler) CreateConnection(ctx context.Context, req *pb.CreateConnectionReq) (*pb.ConnectionResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateConnection(req.Name, req.Provider, req.BaseUrl, req.ApiKey); err != nil {
		return nil, err
	}
	id, err := identity.New()
	if err != nil {
		return nil, errors.ErrInternal
	}
	encrypted, err := crypto.EncryptString(req.ApiKey, h.svcCtx.EncKey)
	if err != nil {
		return nil, errors.ErrInternal
	}
	connection := appModel.ProviderConnection{ID: id, Name: req.Name, Provider: req.Provider, BaseURL: req.BaseUrl, APIKeyEncrypted: encrypted, OwnerType: "user", OwnerID: &owner}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if req.QuotaPoolId != nil {
			var pool appModel.QuotaPool
			if err := ownRecord(tx, owner, *req.QuotaPoolId, &pool, true); err != nil {
				return err
			}
			connection.QuotaPoolID = pool.ID
		} else {
			poolID, err := identity.New()
			if err != nil {
				return errors.ErrInternal
			}
			pool := appModel.QuotaPool{ID: poolID, Name: req.Name + " quota", OwnerType: "user", OwnerID: &owner}
			if err := tx.Create(&pool).Error; err != nil {
				return err
			}
			connection.QuotaPoolID = pool.ID
		}
		return tx.Create(&connection).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	return connectionResponse(connection), nil
}

func (h *LLMGatewayHandler) UpdateConnection(ctx context.Context, req *pb.UpdateConnectionReq) (*pb.ConnectionResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	var current appModel.ProviderConnection
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ownRecord(tx, owner, req.ConnectionId, &current, true); err != nil {
			return err
		}
		if req.Name != nil {
			current.Name = *req.Name
		}
		if req.Provider != nil {
			current.Provider = *req.Provider
		}
		if req.BaseUrl != nil {
			current.BaseURL = *req.BaseUrl
		}
		if err := validateConnection(current.Name, current.Provider, current.BaseURL, req.GetApiKey()); err != nil {
			return err
		}
		if req.QuotaPoolId != nil {
			var pool appModel.QuotaPool
			if err := ownRecord(tx, owner, *req.QuotaPoolId, &pool, true); err != nil {
				return err
			}
			current.QuotaPoolID = pool.ID
		}
		// Provider changes must not invalidate existing pinned judgment models.
		if req.Provider != nil {
			var models []appModel.ModelRegistry
			if err := tx.Where("connection_id = ?", current.ID).Find(&models).Error; err != nil {
				return err
			}
			for _, m := range models {
				if err := validateJudgmentModel(m.Capability, current.Provider, m.ModelName, m.InputPricePerMTok, m.OutputPricePerMTok); err != nil {
					return err
				}
			}
		}
		updates := map[string]any{"name": current.Name, "provider": current.Provider, "base_url": current.BaseURL, "quota_pool_id": current.QuotaPoolID}
		if req.ApiKey != nil {
			encrypted, err := crypto.EncryptString(*req.ApiKey, h.svcCtx.EncKey)
			if err != nil {
				return errors.ErrInternal
			}
			updates["api_key_encrypted"] = encrypted
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	if err := h.svcCtx.ModelRepo.Refresh(); err != nil {
		return nil, managementError(err)
	}
	return connectionResponse(current), nil
}

func (h *LLMGatewayHandler) DeleteConnection(ctx context.Context, req *pb.DeleteConnectionReq) (*pb.DeleteConnectionResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current appModel.ProviderConnection
		if err := ownRecord(tx, owner, req.ConnectionId, &current, true); err != nil {
			return err
		}
		return tx.Delete(&current).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	return &pb.DeleteConnectionResp{Success: true}, nil
}

func (h *LLMGatewayHandler) ListQuotaPools(ctx context.Context, req *pb.ListQuotaPoolsReq) (*pb.ListQuotaPoolsResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	query := h.svcCtx.DB.WithContext(ctx).Model(&appModel.QuotaPool{}).Where("owner_type = ? AND owner_id = ?", "user", owner)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, managementError(err)
	}
	offset, size := managementPage(req.Page, req.PageSize)
	var records []appModel.QuotaPool
	if err := query.Order("created_at, id").Offset(offset).Limit(size).Find(&records).Error; err != nil {
		return nil, managementError(err)
	}
	items := make([]*pb.QuotaPoolResp, 0, len(records))
	for _, pool := range records {
		items = append(items, poolResponse(pool))
	}
	return &pb.ListQuotaPoolsResp{Items: items, Total: int32(total)}, nil
}

func (h *LLMGatewayHandler) CreateQuotaPool(ctx context.Context, req *pb.CreateQuotaPoolReq) (*pb.QuotaPoolResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 256 {
		return nil, errors.ErrInvalidParam
	}
	limits, err := limitValues(req.Limits)
	if err != nil {
		return nil, err
	}
	id, err := identity.New()
	if err != nil {
		return nil, errors.ErrInternal
	}
	pool := appModel.QuotaPool{ID: id, Name: req.Name, OwnerType: "user", OwnerID: &owner, RequestsPerMinute: limits.RequestsPerMinute, RequestsPerSecond: limits.RequestsPerSecond, TokensPerSecond: limits.TokensPerSecond, Concurrency: limits.Concurrency}
	if err := h.svcCtx.DB.WithContext(ctx).Create(&pool).Error; err != nil {
		return nil, managementError(err)
	}
	return poolResponse(pool), nil
}

func (h *LLMGatewayHandler) UpdateQuotaPool(ctx context.Context, req *pb.UpdateQuotaPoolReq) (*pb.QuotaPoolResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := limitValues(req.Limits)
	if err != nil {
		return nil, err
	}
	if req.Name != nil && (strings.TrimSpace(*req.Name) == "" || len(*req.Name) > 256) {
		return nil, errors.ErrInvalidParam
	}
	var current appModel.QuotaPool
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ownRecord(tx, owner, req.QuotaPoolId, &current, true); err != nil {
			return err
		}
		updates := map[string]any{}
		if req.Name != nil {
			current.Name = *req.Name
			updates["name"] = current.Name
		}
		if req.Limits != nil {
			updates = limitUpdates(limits)
			if req.Name != nil {
				updates["name"] = current.Name
			}
			current.RequestsPerMinute, current.RequestsPerSecond, current.TokensPerSecond, current.Concurrency = limits.RequestsPerMinute, limits.RequestsPerSecond, limits.TokensPerSecond, limits.Concurrency
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	if err := h.svcCtx.ModelRepo.Refresh(); err != nil {
		return nil, managementError(err)
	}
	return poolResponse(current), nil
}

func (h *LLMGatewayHandler) DeleteQuotaPool(ctx context.Context, req *pb.DeleteQuotaPoolReq) (*pb.DeleteQuotaPoolResp, error) {
	owner, err := managementOwner(ctx)
	if err != nil {
		return nil, err
	}
	err = h.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current appModel.QuotaPool
		if err := ownRecord(tx, owner, req.QuotaPoolId, &current, true); err != nil {
			return err
		}
		return tx.Delete(&current).Error
	})
	if err != nil {
		return nil, managementError(err)
	}
	return &pb.DeleteQuotaPoolResp{Success: true}, nil
}
