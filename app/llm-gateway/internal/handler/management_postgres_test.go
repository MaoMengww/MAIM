package handler

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/infra"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	migrations "github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Real PostgreSQL is intentional: ownership FKs, row locks, generated owner scope,
// and the production backfill cannot be represented by a mock or SQLite.
func managementPostgres(t *testing.T) (*LLMGatewayHandler, []string, string, string) {
	t.Helper()
	dsn := os.Getenv("AIM_MODEL_MANAGEMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("AIM_MODEL_MANAGEMENT_TEST_DSN is required for isolated PostgreSQL management regressions")
	}
	freshID := func() string { id, err := identity.New(); require.NoError(t, err); return id }
	schema := "management_" + strings.ReplaceAll(freshID(), "-", "")
	admin, err := pgx.Connect(t.Context(), dsn)
	require.NoError(t, err)
	_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, dropErr := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, dropErr)
		require.NoError(t, admin.Close(cleanup))
	})
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	// This is the pre-030 schema boundary; the migration itself is read directly
	// from the embedded deployment source with only its schema name isolated.
	require.NoError(t, db.Exec(`CREATE TABLE model_registry (
		id UUID PRIMARY KEY, model_name TEXT, provider TEXT, capability TEXT, base_url TEXT,
		api_key_encrypted TEXT, context_window BIGINT, max_output_tokens BIGINT,
		input_price_per_mtok DOUBLE PRECISION, output_price_per_mtok DOUBLE PRECISION,
		status TEXT, owner_type TEXT NOT NULL, owner_id UUID, metadata TEXT,
		created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)`).Error)
	owner, foreign := freshID(), freshID()
	ids := []string{freshID(), freshID(), freshID(), freshID()}
	key := make([]byte, 32)
	ciphertext, err := crypto.EncryptString("legacy-fixture-credential", key)
	require.NoError(t, err)
	for i, id := range ids {
		ownerType, status := "platform", "active"
		var ownerID *string
		if i >= 2 {
			ownerType, ownerID = "user", &owner
		}
		if i == 3 {
			status = "disabled"
		}
		require.NoError(t, db.Exec(`INSERT INTO model_registry
			(id,model_name,provider,capability,base_url,api_key_encrypted,input_price_per_mtok,output_price_per_mtok,status,owner_type,owner_id,created_at,updated_at)
			VALUES (?,?,'openai','chat','https://example.invalid/v1',?,2,3,?,?,?,NOW(),NOW())`, id, "same-legacy-model", ciphertext, status, ownerType, ownerID).Error)
	}
	migration, err := migrations.FS.ReadFile("030_model_connections.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`).Error)
	isolated := strings.NewReplacer("llm.", schema+".", "public.schema_migrations", schema+".schema_migrations").Replace(string(migration))
	require.NoError(t, db.Exec(isolated).Error)
	// Docker initialization and application startup both execute the lineage.
	require.NoError(t, db.Exec(isolated).Error)
	var applied int64
	require.NoError(t, db.Table(schema+".schema_migrations").Where("version = ?", "030_model_connections").Count(&applied).Error)
	require.EqualValues(t, 1, applied)
	s := newMockServiceContext()
	s.DB, s.EncKey = &database.DB{DB: db}, key
	repo := infra.NewModelRepo(s.DB, key, time.Hour)
	t.Cleanup(repo.Close)
	s.ModelRepo = repo
	return NewLLMGatewayHandler(s), ids, owner, foreign
}

func ownerManagementContext(t *testing.T, owner string) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), interceptor.ContextKeyUserID, owner)
}

func TestManagementMigrationBackfillsIndependentConnections(t *testing.T) {
	h, ids, owner, _ := managementPostgres(t)
	for _, id := range ids {
		entry, err := h.svcCtx.ModelRepo.FindByID(t.Context(), id)
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Equal(t, id, entry.ConnectionID, "equal model names and credentials must not merge connections")
		require.Equal(t, id, entry.QuotaPoolID, "every platform/user, active/disabled row needs independent supply")
		require.Equal(t, "legacy-fixture-credential", entry.APIKey)
		require.Equal(t, domain.CapacityLimits{}, entry.PoolLimits)
		require.Equal(t, domain.CapacityLimits{}, entry.ModelLimits)
		require.Positive(t, entry.PoolRevision)
		require.Positive(t, entry.ModelRevision)
	}
	listed, err := h.ListConnections(ownerManagementContext(t, owner), &pb.ListConnectionsReq{})
	require.NoError(t, err)
	require.Len(t, listed.Items, 2, "own disabled model connection remains manageable, platform connections never leak")
	models, err := h.ListModels(ownerManagementContext(t, owner), &pb.ListModelsReq{OwnerId: &owner})
	require.NoError(t, err)
	require.Len(t, models.Items, 3)
	for _, item := range models.Items {
		require.NotEqual(t, ids[3], item.Id)
	}
}

func TestManagementIsolatesOwnersAndRejectsReferencedDeletes(t *testing.T) {
	h, _, owner, foreign := managementPostgres(t)
	ctx, otherCtx := ownerManagementContext(t, owner), ownerManagementContext(t, foreign)
	pool, err := h.CreateQuotaPool(ctx, &pb.CreateQuotaPoolReq{Name: "shared supply", Limits: &pb.CapacityLimits{RequestsPerMinute: 7, Concurrency: 2}})
	require.NoError(t, err)
	_, err = h.CreateConnection(otherCtx, &pb.CreateConnectionReq{Name: "stolen pool", Provider: "openai", QuotaPoolId: &pool.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	connection, err := h.CreateConnection(ctx, &pb.CreateConnectionReq{Name: "owned", Provider: "openai", ApiKey: "first-fixture-key", QuotaPoolId: &pool.Id})
	require.NoError(t, err)
	_, err = h.CreateModel(otherCtx, &pb.CreateModelReq{ModelName: "stolen connection", Capability: "chat", OwnerType: "user", OwnerId: &foreign, ConnectionId: connection.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	_, err = h.UpdateConnection(otherCtx, &pb.UpdateConnectionReq{ConnectionId: connection.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	_, err = h.UpdateQuotaPool(otherCtx, &pb.UpdateQuotaPoolReq{QuotaPoolId: pool.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	_, err = h.DeleteConnection(otherCtx, &pb.DeleteConnectionReq{ConnectionId: connection.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	_, err = h.DeleteQuotaPool(otherCtx, &pb.DeleteQuotaPoolReq{QuotaPoolId: pool.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	foreignPool, err := h.CreateQuotaPool(otherCtx, &pb.CreateQuotaPoolReq{Name: "foreign supply"})
	require.NoError(t, err)
	_, err = h.UpdateConnection(ctx, &pb.UpdateConnectionReq{ConnectionId: connection.Id, QuotaPoolId: &foreignPool.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	model, err := h.CreateModel(ctx, &pb.CreateModelReq{ModelName: "owned model", Capability: "chat", OwnerType: "user", OwnerId: &owner, ConnectionId: connection.Id})
	require.NoError(t, err)
	foreignConnection, err := h.CreateConnection(otherCtx, &pb.CreateConnectionReq{Name: "foreign", Provider: "openai"})
	require.NoError(t, err)
	_, err = h.UpdateModel(ctx, &pb.UpdateModelReq{ModelId: model.Id, ConnectionId: &foreignConnection.Id})
	assertErrorCode(t, err, errors.CodeNotFound)
	_, err = h.DeleteConnection(ctx, &pb.DeleteConnectionReq{ConnectionId: connection.Id})
	assertErrorCode(t, err, errors.CodeConflict)
	_, err = h.DeleteQuotaPool(ctx, &pb.DeleteQuotaPoolReq{QuotaPoolId: pool.Id})
	assertErrorCode(t, err, errors.CodeConflict)
	connections, err := h.ListConnections(otherCtx, &pb.ListConnectionsReq{})
	require.NoError(t, err)
	require.Len(t, connections.Items, 1)
	require.Equal(t, foreignConnection.Id, connections.Items[0].Id)
}

func TestManagementSharedConnectionRotationAndSupplyUpdates(t *testing.T) {
	h, _, owner, _ := managementPostgres(t)
	ctx := ownerManagementContext(t, owner)
	connection, err := h.CreateConnection(ctx, &pb.CreateConnectionReq{Name: "independent default", Provider: "openai", BaseUrl: "https://example.invalid/v1", ApiKey: "initial-fixture-key"})
	require.NoError(t, err)
	secondConnection, err := h.CreateConnection(ctx, &pb.CreateConnectionReq{Name: "explicit sharing", Provider: "openai", QuotaPoolId: &connection.QuotaPoolId})
	require.NoError(t, err)
	require.Equal(t, connection.QuotaPoolId, secondConnection.QuotaPoolId)
	var modelIDs []string
	for _, name := range []string{"model-a", "model-b"} {
		created, err := h.CreateModel(ctx, &pb.CreateModelReq{ModelName: name, Capability: "chat", OwnerType: "user", OwnerId: &owner, ConnectionId: connection.Id})
		require.NoError(t, err)
		require.Equal(t, &pb.CapacityLimits{}, created.PoolLimits)
		modelIDs = append(modelIDs, created.Id)
	}
	before, err := h.svcCtx.ModelRepo.FindByID(ctx, modelIDs[0])
	require.NoError(t, err)
	rotated := "rotated-fixture-key"
	response, err := h.UpdateConnection(ctx, &pb.UpdateConnectionReq{ConnectionId: connection.Id, ApiKey: &rotated})
	require.NoError(t, err)
	wire, err := protocol.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(wire), rotated)
	_, err = h.UpdateQuotaPool(ctx, &pb.UpdateQuotaPoolReq{QuotaPoolId: connection.QuotaPoolId, Limits: &pb.CapacityLimits{RequestsPerMinute: 13, RequestsPerSecond: 3, TokensPerSecond: 1000, Concurrency: 4}})
	require.NoError(t, err)
	for _, id := range modelIDs {
		entry, err := h.svcCtx.ModelRepo.FindByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, rotated, entry.APIKey, "all models resolve the current connection credential")
		require.Equal(t, domain.CapacityLimits{RequestsPerMinute: 13, RequestsPerSecond: 3, TokensPerSecond: 1000, Concurrency: 4}, entry.PoolLimits)
		require.Greater(t, entry.PoolRevision, before.PoolRevision)
	}
	updated, err := h.UpdateModel(ctx, &pb.UpdateModelReq{ModelId: modelIDs[0], ModelLimits: &pb.CapacityLimits{Concurrency: 1}})
	require.NoError(t, err)
	require.Equal(t, int32(1), updated.ModelLimits.Concurrency)
	after, err := h.svcCtx.ModelRepo.FindByID(ctx, modelIDs[0])
	require.NoError(t, err)
	require.Greater(t, after.ModelRevision, before.ModelRevision)
	_, err = h.UpdateQuotaPool(ctx, &pb.UpdateQuotaPoolReq{QuotaPoolId: connection.QuotaPoolId, Limits: &pb.CapacityLimits{}})
	require.NoError(t, err)
	_, err = h.UpdateModel(ctx, &pb.UpdateModelReq{ModelId: modelIDs[0], ModelLimits: &pb.CapacityLimits{}})
	require.NoError(t, err)
	after, err = h.svcCtx.ModelRepo.FindByID(ctx, modelIDs[0])
	require.NoError(t, err)
	require.Equal(t, domain.CapacityLimits{}, after.PoolLimits, "explicit zeros clear every supplier limit")
	require.Equal(t, domain.CapacityLimits{}, after.ModelLimits)
	_, err = h.UpdateQuotaPool(ctx, &pb.UpdateQuotaPoolReq{QuotaPoolId: connection.QuotaPoolId, Limits: &pb.CapacityLimits{Concurrency: -1}})
	assertErrorCode(t, err, errors.CodeInvalidParam)
	_, err = h.UpdateModel(ctx, &pb.UpdateModelReq{ModelId: modelIDs[0], ModelLimits: &pb.CapacityLimits{TokensPerSecond: -1}})
	assertErrorCode(t, err, errors.CodeInvalidParam)
}

func TestModelRegistryLookupHonorsCanceledContext(t *testing.T) {
	h, ids, _, _ := managementPostgres(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	entry, err := h.svcCtx.ModelRepo.FindByID(ctx, ids[0])
	require.Nil(t, entry)
	require.ErrorIs(t, err, context.Canceled)
	// Cancellation is scoped to this request, not the shared repository.
	entry, err = h.svcCtx.ModelRepo.FindByID(t.Context(), ids[0])
	require.NoError(t, err)
	require.NotNil(t, entry)
}
