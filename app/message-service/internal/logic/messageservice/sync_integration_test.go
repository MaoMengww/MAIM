//go:build integration

package messageservicelogic

import (
	"os"
	"testing"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/migrations/postgres"
	pkgconfig "github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/require"
)

func syncIntegrationContext(t *testing.T) (*svc.ServiceContext, int64, []int64) {
	t.Helper()
	dsn := os.Getenv("INBOX_TEST_DSN")
	require.NotEmpty(t, dsn)
	db, err := database.NewDB(pkgconfig.DatabaseConfig{DSN: dsn, MaxOpenConn: 10}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.RunMigrations(db.DB, postgres.FS))
	uid := time.Now().UnixNano()
	convs := []int64{uid + 1, uid + 2}
	for _, id := range convs {
		require.NoError(t, db.Exec("INSERT INTO messaging.conversations (id, type, name) VALUES (?, 2, 'sync group')", id).Error)
		require.NoError(t, db.Exec("INSERT INTO messaging.conv_members (id, conv_id, user_id) VALUES (?, ?, ?)", id, id, uid).Error)
	}
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DELETE FROM messaging.conv_settings WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.conv_members WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.conversations WHERE id IN ?", convs).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.messages WHERE conv_id IN ?", convs).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", uid).Error)
	})
	return &svc.ServiceContext{Config: config.Config{Message: config.MessageConfig{MaxPageSize: 100}}, DB: db,
		InboxRepo: repo.NewInboxRepo(db), MessageRepo: repo.NewMessageRepo(db), ConversationRepo: repo.NewConversationRepo(db), ProfileRepo: repo.NewProfileRepo(db)}, uid, convs
}

func syncAppend(t *testing.T, s *svc.ServiceContext, uid, conv, id int64, text string) {
	t.Helper()
	require.NoError(t, s.MessageRepo.Insert(t.Context(), &model.Message{ID: id, ConvID: conv, Seq: id, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": text}}))
	require.NoError(t, s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{{UserID: uid, ConvID: conv, MessageID: id}}))
}

func TestUserSyncCrossConversationPagination(t *testing.T) {
	s, uid, convs := syncIntegrationContext(t)
	// With no checkpoint the caller must receive a rebuild, not a silent tail.
	initial, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.True(t, initial.GetRebuildRequired())
	require.Positive(t, initial.GetNextPosition())
	syncAppend(t, s, uid, convs[0], uid+10, "first")
	syncAppend(t, s, uid, convs[1], uid+11, "second")
	syncAppend(t, s, uid, convs[0], uid+12, "third")
	position := initial.GetNextPosition()
	for i, text := range []string{"first", "second", "third"} {
		page, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: position, Limit: 1})
		require.NoError(t, err)
		require.False(t, page.GetRebuildRequired())
		require.Len(t, page.GetChanges(), 1)
		require.Equal(t, text, page.Changes[0].Message.GetText().GetText())
		require.Equal(t, convs[i%2], page.Changes[0].ConversationId)
		require.Greater(t, page.NextPosition, position)
		require.Equal(t, i < 2, page.HasMore)
		position = page.NextPosition
	}
	empty, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: position, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, empty.Changes)
	require.False(t, empty.HasMore)
	require.Equal(t, position, empty.NextPosition)
}

func TestUserSyncRetentionRebuildKeepsHistory(t *testing.T) {
	s, uid, convs := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	syncAppend(t, s, uid, convs[0], uid+10, "old first")
	syncAppend(t, s, uid, convs[0], uid+11, "old second")
	syncAppend(t, s, uid, convs[1], uid+12, "recent")
	require.NoError(t, s.DB.Exec("UPDATE messaging.inbox_entries SET created_at = ? WHERE user_id = ? AND message_id IN (?, ?)", time.Now().AddDate(0, 0, -31), uid, uid+10, uid+11).Error)
	require.NoError(t, s.ConversationRepo.UpsertSettings(t.Context(), &model.ConvSettings{ID: uid, ConvID: convs[0], UserID: uid, IsPinned: true, IsMuted: true}))
	// Expiry must be detected even before the collector runs.
	expired, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.True(t, expired.RebuildRequired)
	require.Equal(t, "expired_position", expired.RebuildReason)
	require.Len(t, expired.Conversations, 2)
	require.True(t, expired.Conversations[0].Conversation.IsPinned)
	require.True(t, expired.Conversations[0].Conversation.IsMuted)
	require.Equal(t, "old second", expired.Conversations[0].Messages[0].GetText().Text)
	// A longer configured window can still replay the same checkpoint.
	s.Config.Message.InboxRetentionDays = 60
	retained, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.False(t, retained.RebuildRequired)
	require.Equal(t, "old first", retained.Changes[0].Message.GetText().Text)
	s.Config.Message.InboxRetentionDays = 0 // default 30 days
	removed, err := s.InboxRepo.Prune(t.Context(), time.Now().AddDate(0, 0, -30))
	require.NoError(t, err)
	require.Equal(t, int64(2), removed)
	after, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
	require.NoError(t, err)
	require.Equal(t, "expired_position", after.RebuildReason)
	history, err := NewGetMessagesLogic(t.Context(), s).GetMessages(&message.GetMessagesReq{UserId: uid, ConversationId: convs[0], Pagination: &common.CursorPagination{Limit: 10}})
	require.NoError(t, err)
	require.Equal(t, "old second", history.Messages[0].GetText().Text)
	require.Equal(t, "old first", history.Messages[1].GetText().Text)
	syncAppend(t, s, uid, convs[1], uid+13, "after rebuild")
	continuation, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: after.NextPosition})
	require.NoError(t, err)
	require.False(t, continuation.RebuildRequired)
	require.Equal(t, "after rebuild", continuation.Changes[0].Message.GetText().Text)
}

func TestUserSyncDoesNotSkipUncommittedChanges(t *testing.T) {
	s, uid, convs := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.NoError(t, s.MessageRepo.Insert(t.Context(), &model.Message{ID: uid + 10, ConvID: convs[0], Seq: 1, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "delayed"}}))
	require.NoError(t, s.MessageRepo.Insert(t.Context(), &model.Message{ID: uid + 11, ConvID: convs[1], Seq: 1, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "later writer"}}))
	tx := s.DB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	// Hold the same stream lock used by fanout, with an allocated but invisible entry.
	require.NoError(t, tx.Exec("UPDATE messaging.inbox_streams SET position = position + 1 WHERE user_id = ?", uid).Error)
	require.NoError(t, tx.Exec("INSERT INTO messaging.inbox_entries (user_id,position,conv_id,message_id,kind) VALUES (?,?,?,?, 'message.new')", uid, initial.NextPosition+1, convs[0], uid+10).Error)
	type result struct {
		page *message.SyncMessagesResp
		err  error
	}
	reads := make(chan result, 1)
	go func() {
		page, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
		reads <- result{page, err}
	}()
	var before result
	select {
	case before = <-reads:
	case <-time.After(2 * time.Second):
		require.NoError(t, tx.Rollback().Error)
		<-reads
		t.Fatal("sync blocked on an unpublished writer instead of reading the committed snapshot")
	}
	require.NoError(t, before.err)
	require.Empty(t, before.page.Changes)
	require.Equal(t, initial.NextPosition, before.page.NextPosition)
	later := make(chan error, 1)
	go func() {
		later <- s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{{UserID: uid, ConvID: convs[1], MessageID: uid + 11}})
	}()
	require.NoError(t, tx.Commit().Error)
	require.NoError(t, <-later)
	first, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: before.page.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Equal(t, "delayed", first.Changes[0].Message.GetText().Text)
	second, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: first.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.False(t, second.HasMore)
	require.Equal(t, "later writer", second.Changes[0].Message.GetText().Text)
	require.Greater(t, second.NextPosition, first.NextPosition)
}

func TestUserSyncUnavailableReferencesStillAdvance(t *testing.T) {
	s, uid, convs := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	syncAppend(t, s, uid, convs[0], uid+10, "former membership")
	syncAppend(t, s, uid, convs[1], uid+11, "visible")
	syncAppend(t, s, uid, convs[1], uid+12, "removed")
	require.NoError(t, s.ConversationRepo.RemoveMember(t.Context(), convs[0], uid))
	require.NoError(t, s.MessageRepo.Delete(t.Context(), uid+12))
	first, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, first.Changes)
	require.True(t, first.HasMore)
	require.Greater(t, first.NextPosition, initial.NextPosition)
	second, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: first.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "visible", second.Changes[0].Message.GetText().Text)
	require.True(t, second.HasMore)
	last, err := NewSyncMessagesLogic(t.Context(), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: second.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, last.Changes)
	require.False(t, last.HasMore)
	require.Greater(t, last.NextPosition, second.NextPosition)
}
