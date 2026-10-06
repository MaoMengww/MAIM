//go:build integration

package repo

import (
	"os"
	"sync"

	"testing"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/database"
	"github.com/stretchr/testify/require"
)

func inboxIntegrationDB(t *testing.T) (*database.DB, int64, int64, int64) {
	t.Helper()
	dsn := os.Getenv("INBOX_TEST_DSN")
	require.NotEmpty(t, dsn, "INBOX_TEST_DSN must point to an isolated PostgreSQL database")
	db, err := database.NewDB(config.DatabaseConfig{DSN: dsn, MaxOpenConn: 10}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.RunMigrations(db.DB, postgres.FS))
	userID := time.Now().UnixNano()
	firstConv, secondConv := userID+1, userID+2
	for _, convID := range []int64{firstConv, secondConv} {
		require.NoError(t, db.Exec("INSERT INTO messaging.conversations (id) VALUES (?)", convID).Error)
	}
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DELETE FROM messaging.conversations WHERE id IN (?, ?)", firstConv, secondConv).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.messages WHERE conv_id IN (?, ?)", firstConv, secondConv).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", userID).Error)
	})
	return db, userID, firstConv, secondConv
}

func TestInboxCrossConversationPositions(t *testing.T) {
	db, userID, firstConv, secondConv := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	ctx := t.Context()
	firstMsg, secondMsg := userID+3, userID+4
	// Both conversations have seq=1; that must not collide in the user's stream.
	for i, convID := range []int64{firstConv, secondConv} {
		require.NoError(t, db.Exec("INSERT INTO messaging.messages (id, conv_id, seq) VALUES (?, ?, 1)", firstMsg+int64(i), convID).Error)
	}
	first := model.UserInbox{UserID: userID, ConvID: firstConv, MessageID: firstMsg, ChangeID: firstMsg, Kind: model.InboxMessageNew}
	second := model.UserInbox{UserID: userID, ConvID: secondConv, MessageID: secondMsg, ChangeID: secondMsg, Kind: model.InboxMessageNew}
	require.NoError(t, r.BatchInsert(ctx, []model.UserInbox{first}))
	require.NoError(t, r.BatchInsert(ctx, []model.UserInbox{second}))
	// Replay cannot append another entry or advance the committed position.
	require.NoError(t, r.BatchInsert(ctx, []model.UserInbox{first, second, first}))
	var entries []struct {
		Position  int64
		ConvID    int64
		MessageID int64
		Kind      string
	}
	require.NoError(t, db.Raw("SELECT position, conv_id, message_id, kind FROM messaging.inbox_entries WHERE user_id = ? ORDER BY position", userID).Scan(&entries).Error)
	require.Equal(t, []struct {
		Position  int64
		ConvID    int64
		MessageID int64
		Kind      string
	}{{1, firstConv, firstMsg, "message.new"}, {2, secondConv, secondMsg, "message.new"}}, entries)
	var position int64
	require.NoError(t, db.Raw("SELECT position FROM messaging.inbox_streams WHERE user_id = ?", userID).Scan(&position).Error)
	require.Equal(t, int64(2), position)
}

func TestInboxConcurrentFanoutAndReplay(t *testing.T) {
	db, userID, firstConv, secondConv := inboxIntegrationDB(t)
	otherUser := userID + 10
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", otherUser).Error)
	})
	start := make(chan struct{})
	results := make(chan error, 32)
	var writers sync.WaitGroup
	// Two consumers can see the same 16 events in different orders. Both
	// recipients are new streams; reversed batches also exercise lock ordering.
	for i := range 32 {
		writers.Go(func() {
			<-start
			convID := firstConv
			if i%2 != 0 {
				convID = secondConv
			}
			messageID := userID + 100 + int64(i%16)
			entries := []model.UserInbox{
				{UserID: userID, ConvID: convID, MessageID: messageID, ChangeID: messageID, Kind: model.InboxMessageNew},
				{UserID: otherUser, ConvID: convID, MessageID: messageID, ChangeID: messageID, Kind: model.InboxMessageNew},
			}
			if i%2 != 0 {
				entries[0], entries[1] = entries[1], entries[0]
			}
			results <- NewInboxRepo(db).BatchInsert(t.Context(), entries)
		})
	}
	close(start)
	writers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	for _, uid := range []int64{userID, otherUser} {
		var positions []int64
		require.NoError(t, db.Raw("SELECT position FROM messaging.inbox_entries WHERE user_id = ? ORDER BY position", uid).Scan(&positions).Error)
		require.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, positions)
		var position int64
		require.NoError(t, db.Raw("SELECT position FROM messaging.inbox_streams WHERE user_id = ?", uid).Scan(&position).Error)
		require.Equal(t, int64(16), position)
	}
}

func TestInboxRejectsConversationlessAndRollsBack(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	valid := model.UserInbox{UserID: userID, ConvID: convID, MessageID: userID + 100, ChangeID: userID + 100, Kind: model.InboxMessageNew}
	invalid := model.UserInbox{UserID: userID, MessageID: userID + 101, ChangeID: userID + 101, Kind: model.InboxMessageNew}
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{valid, invalid}))
	// Unknown changes must reject the entire batch before allocation.
	invalid.ConvID, invalid.Kind = convID, "unknown"
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{valid, invalid}))
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{valid}))
	var positions []int64
	require.NoError(t, db.Raw("SELECT position FROM messaging.inbox_entries WHERE user_id = ? ORDER BY position", userID).Scan(&positions).Error)
	require.Equal(t, []int64{1}, positions)
	// Bypassing the repo must not bypass the conversation requirement.
	require.Error(t, db.Exec("INSERT INTO messaging.inbox_entries (user_id, position, conv_id, message_id, kind) VALUES (?, 2, 0, ?, 'message.new')", userID, userID+102).Error)
}

func TestPersonalDeletionMigrationSurvivesInboxRetention(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	messageID := userID + 100
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })
	require.NoError(t, tx.Exec(`ALTER TABLE messaging.inbox_entries ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE`).Error)
	require.NoError(t, tx.Exec(`INSERT INTO messaging.inbox_streams (user_id) VALUES (?)`, userID).Error)
	require.NoError(t, tx.Exec(`INSERT INTO messaging.inbox_entries (user_id, position, conv_id, message_id, kind, change_id, is_deleted)
  VALUES (?, 1, ?, ?, 'message.new', ?, TRUE), (?, 2, ?, ?, 'message.edited', ?, TRUE)`, userID, convID, messageID, messageID, userID, convID, messageID, messageID+1).Error)
	migration, err := postgres.FS.ReadFile("018_personal_message_deletions.sql")
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(migration)).Error)
	require.NoError(t, tx.Exec(string(migration)).Error)
	require.NoError(t, tx.Exec("DELETE FROM messaging.inbox_entries WHERE user_id = ?", userID).Error)
	var deletions []model.PersonalMessageDeletion
	require.NoError(t, tx.Where("user_id = ?", userID).Find(&deletions).Error)
	require.Len(t, deletions, 1)
	require.Equal(t, convID, deletions[0].ConvID)
	require.Equal(t, messageID, deletions[0].MessageID)
	require.False(t, tx.Migrator().HasColumn(&model.UserInbox{}, "is_deleted"))
}
