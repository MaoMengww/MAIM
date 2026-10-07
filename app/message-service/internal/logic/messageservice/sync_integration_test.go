//go:build integration

package messageservicelogic

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maomeng/aim/app/message-service/internal/config"
	conversationlogic "github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/migrations/postgres"
	pkgconfig "github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
)

func syncIntegrationContext(t *testing.T) (*svc.ServiceContext, string, []string, map[int]string) {
	t.Helper()
	dsn := os.Getenv("INBOX_TEST_DSN")
	require.NotEmpty(t, dsn)
	db, err := database.NewDB(pkgconfig.DatabaseConfig{DSN: dsn, MaxOpenConn: 10}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.RunMigrations(db.DB, postgres.FS))
	uid, err := identity.New()
	require.NoError(t, err)
	ids := make(map[int]string)
	for i := range 50 {
		ids[i], err = identity.New()
		require.NoError(t, err)
	}
	convs := []string{ids[1], ids[2]}
	slices.Sort(convs)
	for _, id := range convs {
		require.NoError(t, db.Exec("INSERT INTO messaging.conversations (id, type, name) VALUES (?, 2, 'sync group')", id).Error)
		require.NoError(t, db.Exec("INSERT INTO messaging.conv_members (id, conv_id, user_id, member_type) VALUES (?, ?, ?, 'user')", id, id, uid).Error)
	}
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DELETE FROM messaging.sequences WHERE conv_id IN ?", convs).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.conv_settings WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.conv_members WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.conversations WHERE id IN ?", convs).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.messages WHERE conv_id IN ?", convs).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.personal_message_deletions WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_applied_changes WHERE user_id = ?", uid).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.outbox_events WHERE key IN ?", convs).Error)
	})
	return &svc.ServiceContext{Config: config.Config{Message: config.MessageConfig{MaxPageSize: 100, EditWindowSeconds: 120, RecallWindowSeconds: 120}}, DB: db,
		SequenceRepo: &repo.SequenceRepo{}, OutboxRepo: repo.NewOutboxRepo(db), InboxRepo: repo.NewInboxRepo(db), MessageRepo: repo.NewMessageRepo(db), ConversationRepo: repo.NewConversationRepo(db), ProfileRepo: repo.NewProfileRepo(db)}, uid, convs, ids
}

func syncAppend(t *testing.T, s *svc.ServiceContext, uid, conv, id string, text string) {
	t.Helper()
	var seq int64
	require.NoError(t, s.DB.Model(&model.Message{}).Where("conv_id = ?", conv).Select("COALESCE(MAX(seq), 0) + 1").Scan(&seq).Error)
	require.NoError(t, s.DB.WithContext(t.Context()).Create(&model.Message{ID: id, ConvID: conv, Seq: seq, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": text}}).Error)
	require.NoError(t, s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{{UserID: uid, ConvID: conv, MessageID: &id, ChangeID: id, Kind: model.InboxMessageNew}}))
}

func TestUserSyncCrossConversationPagination(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	// With no checkpoint the caller must receive a rebuild, not a silent tail.
	initial, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.True(t, initial.GetRebuildRequired())
	require.Positive(t, initial.GetNextPosition())
	syncAppend(t, s, uid, convs[0], ids[10], "first")
	syncAppend(t, s, uid, convs[1], ids[11], "second")
	syncAppend(t, s, uid, convs[0], ids[12], "third")
	position := initial.GetNextPosition()
	for i, text := range []string{"first", "second", "third"} {
		page, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: position, Limit: 1})
		require.NoError(t, err)
		require.False(t, page.GetRebuildRequired())
		require.Len(t, page.GetChanges(), 1)
		require.Equal(t, text, page.Changes[0].Message.GetText().GetText())
		require.Equal(t, convs[i%2], page.Changes[0].ConversationId)
		require.Greater(t, page.NextPosition, position)
		require.Equal(t, i < 2, page.HasMore)
		position = page.NextPosition
	}
	empty, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: position, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, empty.Changes)
	require.False(t, empty.HasMore)
	require.Equal(t, position, empty.NextPosition)
}

func TestUserSyncRetentionRebuildKeepsHistory(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	syncAppend(t, s, uid, convs[0], ids[10], "old first")
	syncAppend(t, s, uid, convs[0], ids[11], "old second")
	syncAppend(t, s, uid, convs[1], ids[12], "recent")
	require.NoError(t, s.DB.Exec("UPDATE messaging.inbox_entries SET created_at = ? WHERE user_id = ? AND message_id IN (?, ?)", time.Now().AddDate(0, 0, -31), uid, ids[10], ids[11]).Error)
	require.NoError(t, s.ConversationRepo.UpsertSettings(t.Context(), &model.ConvSettings{ID: ids[0], ConvID: convs[0], UserID: uid, IsPinned: true, IsMuted: true}))
	// Expiry must be detected even before the collector runs.
	expired, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.True(t, expired.RebuildRequired)
	require.Equal(t, "expired_position", expired.RebuildReason)
	require.Len(t, expired.Conversations, 2)
	require.True(t, expired.Conversations[0].Conversation.IsPinned)
	require.True(t, expired.Conversations[0].Conversation.IsMuted)
	require.Equal(t, "old second", expired.Conversations[0].Messages[0].GetText().Text)
	// A longer configured window can still replay the same checkpoint.
	s.Config.Message.InboxRetentionDays = 60
	retained, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.False(t, retained.RebuildRequired)
	require.Equal(t, "old first", retained.Changes[0].Message.GetText().Text)
	s.Config.Message.InboxRetentionDays = 0 // default 30 days
	removed, err := s.InboxRepo.Prune(t.Context(), time.Now().AddDate(0, 0, -30))
	require.NoError(t, err)
	require.Equal(t, int64(2), removed)
	after, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
	require.NoError(t, err)
	require.Equal(t, "expired_position", after.RebuildReason)
	history, err := NewGetMessagesLogic(callerContext(t.Context(), uid), s).GetMessages(&message.GetMessagesReq{UserId: uid, ConversationId: convs[0], Pagination: &message.MessagePagination{Limit: 10}})
	require.NoError(t, err)
	require.Equal(t, "old second", history.Messages[0].GetText().Text)
	require.Equal(t, "old first", history.Messages[1].GetText().Text)
	syncAppend(t, s, uid, convs[1], ids[13], "after rebuild")
	continuation, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: after.NextPosition})
	require.NoError(t, err)
	require.False(t, continuation.RebuildRequired)
	require.Equal(t, "after rebuild", continuation.Changes[0].Message.GetText().Text)
}

func TestUserSyncDoesNotSkipUncommittedChanges(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.NoError(t, s.DB.WithContext(t.Context()).Create(&[]model.Message{
		{ID: ids[10], ConvID: convs[0], Seq: 1, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "delayed"}},
		{ID: ids[11], ConvID: convs[1], Seq: 1, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "later writer"}},
	}).Error)
	tx := s.DB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	// Hold the same stream lock used by fanout, with an allocated but invisible entry.
	require.NoError(t, tx.Exec("UPDATE messaging.inbox_streams SET position = position + 1 WHERE user_id = ?", uid).Error)
	require.NoError(t, tx.Exec("INSERT INTO messaging.inbox_entries (user_id,position,conv_id,message_id,change_id,kind) VALUES (?,?,?,?,?, 'message.new')", uid, initial.NextPosition+1, convs[0], ids[10], ids[10]).Error)
	type result struct {
		page *message.SyncMessagesResp
		err  error
	}
	reads := make(chan result, 1)
	go func() {
		page, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
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
		later <- s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{{UserID: uid, ConvID: convs[1], MessageID: ptr(ids[11]), ChangeID: ids[11], Kind: model.InboxMessageNew}})
	}()
	require.NoError(t, tx.Commit().Error)
	require.NoError(t, <-later)
	first, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: before.page.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Equal(t, "delayed", first.Changes[0].Message.GetText().Text)
	second, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: first.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.False(t, second.HasMore)
	require.Equal(t, "later writer", second.Changes[0].Message.GetText().Text)
	require.Greater(t, second.NextPosition, first.NextPosition)
}

func TestUserSyncUnavailableReferencesStillAdvance(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	initial, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	syncAppend(t, s, uid, convs[0], ids[10], "former membership")
	syncAppend(t, s, uid, convs[1], ids[11], "visible")
	syncAppend(t, s, uid, convs[1], ids[12], "removed")
	require.NoError(t, s.ConversationRepo.RemoveMember(t.Context(), convs[0], uid))
	require.NoError(t, s.DB.WithContext(t.Context()).Where("id = ?", ids[12]).Delete(&model.Message{}).Error)
	first, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, first.Changes)
	require.True(t, first.HasMore)
	require.Greater(t, first.NextPosition, initial.NextPosition)
	second, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: first.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "visible", second.Changes[0].Message.GetText().Text)
	require.True(t, second.HasMore)
	last, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: second.NextPosition, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, model.InboxMessageDeleted, last.Changes[0].Kind)
	require.Equal(t, ids[12], last.Changes[0].GetMessageId())
	require.Nil(t, last.Changes[0].Message)
	require.False(t, last.HasMore)
	require.Greater(t, last.NextPosition, second.NextPosition)
}

// The public logic seam must converge stale new/edit references to tombstones,
// then keep account state after the entire inbox prefix has been collected.
func TestPersonalDeletionSurvivesRetentionAndRebuild(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	convID, sender := convs[0], ids[20]
	require.NoError(t, s.DB.Exec("INSERT INTO messaging.conv_members (id, conv_id, user_id, member_type) VALUES (?, ?, ?, 'user')", sender, convID, sender).Error)
	t.Cleanup(func() {
		require.NoError(t, s.DB.Exec("DELETE FROM messaging.conv_members WHERE user_id = ?", sender).Error)
	})
	s.OutboxRepo = repo.NewOutboxRepo(s.DB)
	initial, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	syncAppend(t, s, uid, convID, ids[10], "visible fallback")
	syncAppend(t, s, uid, convID, ids[11], "private deleted tail")
	require.NoError(t, s.DB.Exec("UPDATE messaging.messages SET sender_id = ? WHERE conv_id = ?", sender, convID).Error)
	require.NoError(t, s.DB.Exec("UPDATE messaging.conversations SET last_message_id = ?, last_message_preview = 'private deleted tail', max_seq = ? WHERE id = ?", ids[11], int64(2), convID).Error)
	request := &message.DeleteMessageReq{UserId: uid, ConversationId: &convID, MessageId: ids[11]}
	// A recipient may delete another sender's message personally, never globally.
	_, err = NewDeleteMessageLogic(callerContext(t.Context(), uid), s).DeleteMessage(&message.DeleteMessageReq{UserId: uid, ConversationId: &convID, MessageId: ids[11], DeleteForAll: true})
	require.Error(t, err)
	_, err = NewDeleteMessageLogic(callerContext(t.Context(), uid), s).DeleteMessage(request)
	require.NoError(t, err)
	_, err = NewDeleteMessageLogic(callerContext(t.Context(), uid), s).DeleteMessage(request)
	require.NoError(t, err)
	var outbox []model.OutboxEvent
	require.NoError(t, s.DB.Where("key = ?", convID).Find(&outbox).Error)
	require.Len(t, outbox, 1)
	require.Equal(t, consts.KafkaTopicMessageCreated, outbox[0].Topic)
	var change event.InboxChangeEvent
	require.NoError(t, outbox[0].UnmarshalPayload(&change))
	require.Equal(t, []string{uid}, change.RecipientIDs)
	require.False(t, change.DeleteForAll)
	require.Nil(t, change.Content)
	entry := model.UserInbox{UserID: uid, ConvID: convID, MessageID: ptr(ids[11]), ChangeID: change.ChangeID, Kind: change.Kind}
	require.NoError(t, s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{entry}))
	require.NoError(t, s.InboxRepo.BatchInsert(t.Context(), []model.UserInbox{entry}))
	delta, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
	require.NoError(t, err)
	require.Len(t, delta.Changes, 3)
	for _, item := range delta.Changes[1:] {
		require.Equal(t, model.InboxMessageDeleted, item.Kind)
		require.Equal(t, ids[11], item.GetMessageId())
		require.Nil(t, item.Message)
	}
	history, err := NewGetMessagesLogic(callerContext(t.Context(), uid), s).GetMessages(&message.GetMessagesReq{UserId: uid, ConversationId: convID, Pagination: &message.MessagePagination{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, history.Messages, 1)
	require.Equal(t, ids[10], history.Messages[0].MessageId)
	require.False(t, history.Pagination.HasMore)
	conv, err := conversationlogic.NewGetConversationLogic(callerContext(t.Context(), uid), s).GetConversation(&message.GetConversationReq{UserId: uid, ConversationId: convID})
	require.NoError(t, err)
	require.Equal(t, ids[10], conv.Conversation.GetLastMessageId())
	require.Equal(t, "visible fallback", conv.Conversation.LastMessagePreview)
	require.Equal(t, int32(1), conv.Conversation.UnreadCount)
	peer, err := NewGetMessagesLogic(callerContext(t.Context(), sender), s).GetMessages(&message.GetMessagesReq{UserId: sender, ConversationId: convID, Pagination: &message.MessagePagination{Limit: 10}})
	require.NoError(t, err)
	require.Len(t, peer.Messages, 2)
	require.Equal(t, "private deleted tail", peer.Messages[0].GetText().Text)
	require.NoError(t, s.DB.Exec("UPDATE messaging.inbox_entries SET created_at = ? WHERE user_id = ?", time.Now().AddDate(0, 0, -31), uid).Error)
	removed, err := s.InboxRepo.Prune(t.Context(), time.Now().AddDate(0, 0, -30))
	require.NoError(t, err)
	require.Equal(t, int64(3), removed)
	rebuilt, err := NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: initial.NextPosition})
	require.NoError(t, err)
	require.Equal(t, "expired_position", rebuilt.RebuildReason)
	require.Len(t, rebuilt.Conversations[0].Messages, 1)
	require.Equal(t, ids[10], rebuilt.Conversations[0].Messages[0].MessageId)
	// Losing and rejoining membership does not remove account deletion state.
	require.NoError(t, s.ConversationRepo.RemoveMember(t.Context(), convID, uid))
	require.NoError(t, s.DB.Exec("INSERT INTO messaging.conv_members (id, conv_id, user_id, member_type) VALUES (?, ?, ?, 'user')", convID, convID, uid).Error)
	rebuilt, err = NewSyncMessagesLogic(callerContext(t.Context(), uid), s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.Len(t, rebuilt.Conversations[0].Messages, 1)
	require.Equal(t, ids[10], rebuilt.Conversations[0].Messages[0].MessageId)
	// If publication fails, the overlay must roll back with it.
	s.OutboxRepo = repo.NewOutboxRepo(s.DB)
	rolledBack := s.DB.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
		inserted, err := s.MessageRepo.InsertPersonalDeletion(t.Context(), tx, uid, convID, ids[10])
		require.NoError(t, err)
		require.True(t, inserted)
		return s.OutboxRepo.Insert(t.Context(), tx, &model.OutboxEvent{ID: outbox[0].ID, Topic: consts.KafkaTopicMessageCreated, Key: convID, ConvID: convID, PublicationSequence: 2, Payload: outbox[0].Payload})
	})
	require.Error(t, rolledBack)
	_, err = s.MessageRepo.ForUser(uid).GetByID(t.Context(), ids[10])
	require.NoError(t, err)
}

func ptr(value string) *string { return &value }

func callerContext(ctx context.Context, userID string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("user-id", userID))
}

func textSubmission(userID, convID, key, text string) *message.SendMessageReq {
	return &message.SendMessageReq{FromUserId: userID, ConversationId: convID, ClientMsgId: key,
		Type:    message.MessageType_MESSAGE_TYPE_TEXT,
		Content: &message.SendMessageReq_Text{Text: &message.TextContent{Text: text}}}
}

func TestConcurrentSubmissionReplayAndOriginalSemantics(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	ctx, cancel := context.WithTimeout(callerContext(t.Context(), uid), 10*time.Second)
	defer cancel()
	convID := convs[0]
	key := uuid.NewString()
	request := textSubmission(uid, convID, key, "original")
	// Hold the authoritative serialization boundary while both callers start.
	barrier := s.DB.WithContext(ctx).Begin()
	require.NoError(t, barrier.Error)
	defer barrier.Rollback()
	require.NoError(t, barrier.Exec("SELECT id FROM messaging.conversations WHERE id = ? FOR UPDATE", convID).Error)
	var blocker int
	require.NoError(t, barrier.Raw("SELECT pg_backend_pid()").Scan(&blocker).Error)
	type result struct {
		response *message.SendMessageResp
		err      error
	}
	results := make(chan result, 2)
	started := make(chan struct{}, 2)
	for range 2 {
		go func() {
			started <- struct{}{}
			response, err := NewSendMessageLogic(ctx, s).SendMessage(request)
			results <- result{response, err}
		}()
	}
	<-started
	<-started
	// Wait for both transactions to be queued behind the held database lock;
	// scheduling or a fast first submission cannot turn this into a serial test.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		require.NoError(t, s.DB.WithContext(ctx).Raw(`WITH RECURSIVE blocked(pid) AS (
			SELECT ?::integer UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid = ANY(pg_blocking_pids(a.pid))
		) SELECT count(*) - 1 FROM blocked`, blocker).Scan(&blocked).Error)
		if blocked == 2 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("concurrent submissions did not reach the transaction barrier")
		}
	}
	require.NoError(t, barrier.Commit().Error)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.response.MessageId, second.response.MessageId)
	require.Equal(t, first.response.Seq, second.response.Seq)
	require.Equal(t, first.response.CreatedAt, second.response.CreatedAt)
	require.NoError(t, identity.Validate(first.response.MessageId))
	request.GetText().MentionUserIds = []string{}
	emptyMentions, err := NewSendMessageLogic(ctx, s).SendMessage(request)
	require.NoError(t, err)
	require.Equal(t, first.response.MessageId, emptyMentions.MessageId)
	history, err := NewGetMessagesLogic(ctx, s).GetMessages(&message.GetMessagesReq{UserId: uid, ConversationId: convID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 1)
	conv, err := conversationlogic.NewGetConversationLogic(ctx, s).GetConversation(&message.GetConversationReq{UserId: uid, ConversationId: convID})
	require.NoError(t, err)
	require.Equal(t, int64(1), conv.Conversation.MaxSeq)
	require.Equal(t, first.response.MessageId, conv.Conversation.GetLastMessageId())
	var publications []model.OutboxEvent
	require.NoError(t, s.DB.Where("conv_id = ?", convID).Find(&publications).Error)
	require.Len(t, publications, 1)
	_, err = NewSendMessageLogic(ctx, s).SendMessage(textSubmission(uid, convID, key, "different"))
	require.ErrorIs(t, err, ErrContentConflict)
	_, err = NewEditMessageLogic(ctx, s).EditMessage(&message.EditMessageReq{UserId: uid, MessageId: first.response.MessageId, Text: &message.TextContent{Text: "edited later"}})
	require.NoError(t, err)
	replayed, err := NewSendMessageLogic(ctx, s).SendMessage(request)
	require.NoError(t, err)
	require.Equal(t, first.response.MessageId, replayed.MessageId)
	require.Equal(t, first.response.Seq, replayed.Seq)
	require.Equal(t, first.response.CreatedAt, replayed.CreatedAt)
	_, err = NewSendMessageLogic(ctx, s).SendMessage(textSubmission(uid, convID, key, "edited later"))
	require.ErrorIs(t, err, ErrContentConflict)
	// Replay is not a capability: a former member cannot obtain its old result.
	require.NoError(t, s.ConversationRepo.RemoveMember(ctx, convID, uid))
	_, err = NewSendMessageLogic(ctx, s).SendMessage(request)
	require.ErrorIs(t, err, ErrNotMember)
	require.NoError(t, s.DB.Exec("INSERT INTO messaging.conv_members (id,conv_id,user_id,member_type) VALUES (?,?,?,'user')", ids[49], convID, uid).Error)
	// Same client key in a separate conversation is an independent submission.
	independent, err := NewSendMessageLogic(ctx, s).SendMessage(textSubmission(uid, convs[1], key, "other conversation"))
	require.NoError(t, err)
	require.NotEqual(t, first.response.MessageId, independent.MessageId)
	// A different sender in the same conversation does not share its key scope.
	peer := ids[20]
	require.NoError(t, s.DB.Exec("INSERT INTO messaging.conv_members (id,conv_id,user_id,member_type) VALUES (?,?,?,'user')", ids[21], convID, peer).Error)
	t.Cleanup(func() {
		require.NoError(t, s.DB.Exec("DELETE FROM messaging.conv_members WHERE user_id = ?", peer).Error)
	})
	peerResponse, err := NewSendMessageLogic(callerContext(ctx, peer), s).SendMessage(textSubmission(peer, convID, key, "peer submission"))
	require.NoError(t, err)
	require.NotEqual(t, first.response.MessageId, peerResponse.MessageId)
	// Reply absence is part of the immutable submitted meaning; a foreign reply
	// is rejected without reserving the action identity.
	foreign := textSubmission(uid, convID, uuid.NewString(), "reply")
	foreign.ReplyToId = &independent.MessageId
	_, err = NewSendMessageLogic(ctx, s).SendMessage(foreign)
	require.Error(t, err)
	foreign.ReplyToId = &first.response.MessageId
	reply, err := NewSendMessageLogic(ctx, s).SendMessage(foreign)
	require.NoError(t, err)
	foreign.ReplyToId = nil
	_, err = NewSendMessageLogic(ctx, s).SendMessage(foreign)
	require.ErrorIs(t, err, ErrContentConflict)
	require.NotEqual(t, first.response.MessageId, reply.MessageId)
	// Persisted custom JSON ignores object key order, not content values.
	custom := &message.SendMessageReq{FromUserId: uid, ConversationId: convID, ClientMsgId: uuid.NewString(), Type: message.MessageType_MESSAGE_TYPE_CUSTOM,
		Content: &message.SendMessageReq_Custom{Custom: &message.CustomContent{Type: "card", Data: `{"a":1,"b":{"x":2,"y":3}}`}}}
	card, err := NewSendMessageLogic(ctx, s).SendMessage(custom)
	require.NoError(t, err)
	custom.GetCustom().Data = `{"b":{"y":3,"x":2},"a":1}`
	cardReplay, err := NewSendMessageLogic(ctx, s).SendMessage(custom)
	require.NoError(t, err)
	require.Equal(t, card.MessageId, cardReplay.MessageId)
	custom.GetCustom().Data = `{"a":2,"b":{"x":2,"y":3}}`
	_, err = NewSendMessageLogic(ctx, s).SendMessage(custom)
	require.ErrorIs(t, err, ErrContentConflict)
	// JSONB normalizes numeric lexemes. Equality must preserve exact values,
	// including integers beyond float64's consecutive-integer range.
	numeric := &message.SendMessageReq{FromUserId: uid, ConversationId: convID, ClientMsgId: uuid.NewString(), Type: message.MessageType_MESSAGE_TYPE_CUSTOM,
		Content: &message.SendMessageReq_Custom{Custom: &message.CustomContent{Type: "card", Data: `{"large":9007199254740993,"exponent":1e2,"small":1e-7}`}}}
	numericOriginal, err := NewSendMessageLogic(ctx, s).SendMessage(numeric)
	require.NoError(t, err)
	numericReplay, err := NewSendMessageLogic(ctx, s).SendMessage(numeric)
	require.NoError(t, err)
	require.Equal(t, numericOriginal.MessageId, numericReplay.MessageId)
	numeric.GetCustom().Data = `{"large":9007199254740993.0,"exponent":100.00,"small":0.0000001}`
	numericDecimal, err := NewSendMessageLogic(ctx, s).SendMessage(numeric)
	require.NoError(t, err)
	require.Equal(t, numericOriginal.MessageId, numericDecimal.MessageId)
	numeric.GetCustom().Data = `{"large":9007199254740992,"exponent":100,"small":0.0000001}`
	_, err = NewSendMessageLogic(ctx, s).SendMessage(numeric)
	require.ErrorIs(t, err, ErrContentConflict)
	location := &message.SendMessageReq{FromUserId: uid, ConversationId: convID, ClientMsgId: uuid.NewString(), Type: message.MessageType_MESSAGE_TYPE_LOCATION,
		Content: &message.SendMessageReq_Location{Location: &message.LocationContent{Latitude: 1e-7, Longitude: -1e-7}}}
	locationOriginal, err := NewSendMessageLogic(ctx, s).SendMessage(location)
	require.NoError(t, err)
	locationReplay, err := NewSendMessageLogic(ctx, s).SendMessage(location)
	require.NoError(t, err)
	require.Equal(t, locationOriginal.MessageId, locationReplay.MessageId)
	location.GetLocation().Latitude = 2e-7
	_, err = NewSendMessageLogic(ctx, s).SendMessage(location)
	require.ErrorIs(t, err, ErrContentConflict)
	// Non-JSON custom text is not the JSON document containing that string.
	rawText := &message.SendMessageReq{FromUserId: uid, ConversationId: convID, ClientMsgId: uuid.NewString(), Type: message.MessageType_MESSAGE_TYPE_CUSTOM,
		Content: &message.SendMessageReq_Custom{Custom: &message.CustomContent{Type: "card", Data: "hello"}}}
	_, err = NewSendMessageLogic(ctx, s).SendMessage(rawText)
	require.NoError(t, err)
	rawText.GetCustom().Data = `"hello"`
	_, err = NewSendMessageLogic(ctx, s).SendMessage(rawText)
	require.ErrorIs(t, err, ErrContentConflict)
}

func TestSubmissionSequenceExhaustionRollsBackAndLeavesKeyReusable(t *testing.T) {
	for _, boundary := range []string{"message", "publication"} {
		t.Run(boundary, func(t *testing.T) {
			s, uid, convs, _ := syncIntegrationContext(t)
			ctx := callerContext(t.Context(), uid)
			convID := convs[0]
			if boundary == "message" {
				require.NoError(t, s.DB.Exec("INSERT INTO messaging.sequences (conv_id,current_seq) VALUES (?,?)", convID, sequence.Max-1).Error)
				require.NoError(t, s.DB.Exec("UPDATE messaging.conversations SET max_seq = ? WHERE id = ?", sequence.Max-1, convID).Error)
			} else {
				require.NoError(t, s.DB.Exec("UPDATE messaging.conversations SET publication_sequence = ? WHERE id = ?", sequence.Max-1, convID).Error)
			}
			last, err := NewSendMessageLogic(ctx, s).SendMessage(textSubmission(uid, convID, uuid.NewString(), "last safe append"))
			require.NoError(t, err)
			if boundary == "message" {
				require.Equal(t, sequence.Max, last.Seq)
			}
			pending := textSubmission(uid, convID, uuid.NewString(), "retry after repair")
			_, err = NewSendMessageLogic(ctx, s).SendMessage(pending)
			require.Error(t, err)
			history, err := NewGetMessagesLogic(ctx, s).GetMessages(&message.GetMessagesReq{UserId: uid, ConversationId: convID})
			require.NoError(t, err)
			require.Len(t, history.Messages, 1)
			require.Equal(t, last.MessageId, history.Messages[0].MessageId)
			var conv model.Conversation
			require.NoError(t, s.DB.Where("id = ?", convID).Take(&conv).Error)
			require.Equal(t, last.Seq, conv.MaxSeq)
			require.Equal(t, last.MessageId, *conv.LastMessageID)
			var publication []model.OutboxEvent
			require.NoError(t, s.DB.Where("conv_id = ?", convID).Find(&publication).Error)
			require.Len(t, publication, 1)
			if boundary == "publication" {
				require.Equal(t, sequence.Max, publication[0].PublicationSequence)
			}
			// Repair this isolated fixture, then reuse the failed action identity.
			if boundary == "message" {
				require.NoError(t, s.DB.Exec("UPDATE messaging.messages SET seq = ? WHERE id = ?", sequence.Max-2, last.MessageId).Error)
				require.NoError(t, s.DB.Exec("UPDATE messaging.conversations SET max_seq = ? WHERE id = ?", sequence.Max-2, convID).Error)
				require.NoError(t, s.DB.Exec("UPDATE messaging.sequences SET current_seq = ? WHERE conv_id = ?", sequence.Max-2, convID).Error)
			} else {
				require.NoError(t, s.DB.Exec("UPDATE messaging.conversations SET publication_sequence = ? WHERE id = ?", sequence.Max-2, convID).Error)
			}
			retry, err := NewSendMessageLogic(ctx, s).SendMessage(pending)
			require.NoError(t, err)
			require.NotEqual(t, last.MessageId, retry.MessageId)
			if boundary == "publication" {
				require.Equal(t, last.Seq+1, retry.Seq)
			} else {
				require.Equal(t, sequence.Max-1, retry.Seq)
			}
		})
	}
}

func TestSyncRejectsInvalidAndRebuildsUnknownPositions(t *testing.T) {
	s, uid, _, _ := syncIntegrationContext(t)
	ctx := callerContext(t.Context(), uid)
	for _, position := range []int64{-1, sequence.Max + 1} {
		_, err := NewSyncMessagesLogic(ctx, s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: position})
		require.Error(t, err)
	}
	page, err := NewSyncMessagesLogic(ctx, s).SyncMessages(&message.SyncMessagesReq{UserId: uid, Position: sequence.Max})
	require.NoError(t, err)
	require.True(t, page.RebuildRequired)
}
