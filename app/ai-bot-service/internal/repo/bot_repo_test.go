package repo

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupMockDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	return &database.DB{DB: gormDB}, mock
}

func TestBotRepo_FindByID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBotRepo(db, nil)

	rows := sqlmock.NewRows([]string{
		"id", "owner_id", "name", "avatar", "type",
		"status", "use_platform_model", "model_name", "base_url",
		"api_key_encrypted", "system_prompt", "persona", "enable_knowledge",
		"temperature", "max_context_messages", "streaming_enabled",
		"memory_model_name", "memory_use_platform_model", "memory_api_key_encrypted",
		"conn_mode", "webhook_secret", "callback_url", "app_secret_hash",
		"bot_tags", "capabilities", "settings", "created_at", "updated_at",
	}).AddRow(
		1, 100, "test-bot", "", "platform",
		"active", true, "gpt-4o", "",
		"", "You are {bot_name}", "friendly", true,
		0.7, 10, true,
		"gpt-4o-mini", true, "",
		"", "", "", "",
		"[]", "{}", "{}", nil, nil,
	)

	mock.ExpectQuery(`SELECT \* FROM "bots" WHERE id = \$1.*ORDER BY "bots"\."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	bot, err := repo.FindByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), bot.ID)
	assert.Equal(t, "test-bot", bot.Name)
	assert.Equal(t, "platform", bot.Type)
	assert.True(t, bot.UsePlatformModel)
	assert.Equal(t, "gpt-4o", bot.ModelName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBotRepo_FindByID_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBotRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "bots" WHERE id = \$1`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.FindByID(context.Background(), 999)
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConvBotRepo_FindByBotAndConv(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewConvBotRepo(db)

	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "bot_id", "added_by", "response_triggers", "bot_settings", "created_at",
	}).AddRow(
		1, 456, 1001, 123, `["mention"]`, nil, nil,
	)

	mock.ExpectQuery(`SELECT \* FROM "conv"."conv_bots" WHERE bot_id = \$1 AND conv_id = \$2`).
		WithArgs(int64(1001), int64(456), 1).
		WillReturnRows(rows)

	cb, err := repo.FindByBotAndConv(context.Background(), 1001, 456)
	require.NoError(t, err)
	assert.Equal(t, int64(456), cb.ConvID)
	assert.Equal(t, int64(1001), cb.BotID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConvBotRepo_FindByBotAndConv_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewConvBotRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "conv"."conv_bots" WHERE bot_id = \$1 AND conv_id = \$2`).
		WithArgs(int64(999), int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.FindByBotAndConv(context.Background(), 999, 999)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Compile-time interface check
func TestModelStructs(t *testing.T) {
	var _ = model.Bot{}
	var _ = model.ConvBot{}
	var _ = model.Memory{}
}
