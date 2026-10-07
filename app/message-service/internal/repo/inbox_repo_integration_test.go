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
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storageID(t *testing.T) string {
	t.Helper()
	id, err := identity.New()
	require.NoError(t, err)
	return id
}

func inboxIntegrationDB(t *testing.T) (*database.DB, string, string, string) {
	t.Helper()
	dsn := os.Getenv("INBOX_TEST_DSN")
	require.NotEmpty(t, dsn, "INBOX_TEST_DSN must point to an isolated PostgreSQL database")
	db, err := database.NewDB(config.DatabaseConfig{DSN: dsn, MaxOpenConn: 10}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.RunMigrations(db.DB, postgres.FS))
	userID, firstConv, secondConv := storageID(t), storageID(t), storageID(t)
	for _, convID := range []string{firstConv, secondConv} {
		require.NoError(t, db.Exec("INSERT INTO messaging.conversations (id) VALUES (?)", convID).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"outbox_events", "personal_message_deletions", "conv_read_seqs", "conv_settings", "conv_members", "messages", "sequences"} {
			require.NoError(t, db.Exec("DELETE FROM messaging."+table+" WHERE conv_id IN (?, ?)", firstConv, secondConv).Error)
		}
		require.NoError(t, db.Exec("DELETE FROM messaging.conversations WHERE id IN (?, ?)", firstConv, secondConv).Error)
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", userID).Error)
	})
	return db, userID, firstConv, secondConv
}

func TestInboxCrossConversationPositions(t *testing.T) {
	db, userID, firstConv, secondConv := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	firstMsg, secondMsg := storageID(t), storageID(t)
	for i, convID := range []string{firstConv, secondConv} {
		messageID := []string{firstMsg, secondMsg}[i]
		require.NoError(t, db.Exec("INSERT INTO messaging.messages (id, conv_id, seq) VALUES (?, ?, 1)", messageID, convID).Error)
	}
	first := model.UserInbox{UserID: userID, ConvID: firstConv, MessageID: &firstMsg, ChangeID: storageID(t), Kind: model.InboxMessageNew}
	second := model.UserInbox{UserID: userID, ConvID: secondConv, MessageID: &secondMsg, ChangeID: storageID(t), Kind: model.InboxMessageNew}
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{first}))
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{second}))
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{first, second, first}))
	page, err := r.ReadPage(t.Context(), userID, 1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Equal(t, int64(2), page.NextPosition)
	require.Equal(t, []model.UserInbox{second}, withoutInboxTimestamps(page.Entries))
}

func withoutInboxTimestamps(entries []model.UserInbox) []model.UserInbox {
	for i := range entries {
		entries[i].CreatedAt = time.Time{}
		entries[i].Position = 0
	}
	return entries
}

func TestInboxConcurrentFanoutAndReplay(t *testing.T) {
	db, userID, firstConv, secondConv := inboxIntegrationDB(t)
	otherUser := storageID(t)
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DELETE FROM messaging.inbox_streams WHERE user_id = ?", otherUser).Error)
	})
	messages, changes := make([]string, 16), make([]string, 16)
	for i := range 16 {
		messages[i], changes[i] = storageID(t), storageID(t)
	}
	start := make(chan struct{})
	results := make(chan error, 32)
	var writers sync.WaitGroup
	for i := range 32 {
		writers.Go(func() {
			<-start
			convID := firstConv
			if i%2 != 0 {
				convID = secondConv
			}
			messageID := messages[i%16]
			entries := []model.UserInbox{
				{UserID: userID, ConvID: convID, MessageID: &messageID, ChangeID: changes[i%16], Kind: model.InboxMessageNew},
				{UserID: otherUser, ConvID: convID, MessageID: &messageID, ChangeID: changes[i%16], Kind: model.InboxMessageNew},
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
	for _, uid := range []string{userID, otherUser} {
		var positions []int64
		require.NoError(t, db.Raw("SELECT position FROM messaging.inbox_entries WHERE user_id = ? ORDER BY position", uid).Scan(&positions).Error)
		require.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, positions)
		page, err := NewInboxRepo(db).ReadPage(t.Context(), uid, 16, 10, time.Now().Add(-time.Hour))
		require.NoError(t, err)
		require.Equal(t, int64(16), page.NextPosition)
		require.Empty(t, page.Entries)
	}
}

func TestInboxRejectsConversationlessAndRollsBack(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	messageID := storageID(t)
	valid := model.UserInbox{UserID: userID, ConvID: convID, MessageID: &messageID, ChangeID: storageID(t), Kind: model.InboxMessageNew}
	invalid := model.UserInbox{UserID: userID, MessageID: &messageID, ChangeID: storageID(t), Kind: model.InboxMessageNew}
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{valid, invalid}))
	invalid.ConvID, invalid.Kind = convID, "unknown"
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{valid, invalid}))
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{valid}))
	page, err := r.ReadPage(t.Context(), userID, 1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), page.NextPosition)
	require.Error(t, db.Exec("INSERT INTO messaging.inbox_entries (user_id, position, conv_id, message_id, change_id, kind) VALUES (?, 2, ?, ?, ?, 'message.new')", userID, "00000000-0000-0000-0000-000000000000", messageID, storageID(t)).Error)
}

func TestInboxSafeIntegerExhaustionRollsBackEntireBatch(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	require.NoError(t, db.Create(&model.InboxStream{UserID: userID, Position: sequence.Max - 1, RetainedPosition: sequence.Max - 1}).Error)
	first := model.UserInbox{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxConversationUpsert}
	second := model.UserInbox{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxConversationRemoved}
	// A later allocation failure must roll back the preceding entry and its dedup key.
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{first, second}))
	page, err := r.ReadPage(t.Context(), userID, sequence.Max-1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, sequence.Max-1, page.NextPosition)
	require.Empty(t, page.Entries)
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{first}))
	page, err = r.ReadPage(t.Context(), userID, sequence.Max-1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Len(t, page.Entries, 1)
	require.Equal(t, sequence.Max, page.NextPosition)
	require.Equal(t, first.ChangeID, page.Entries[0].ChangeID)
	require.Equal(t, sequence.Max, page.Entries[0].Position)
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{first}))
	require.Error(t, r.BatchInsert(t.Context(), []model.UserInbox{second}))
}

func TestInboxUnknownHolesRetentionAndCheckpoint(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	require.NoError(t, r.EnsureStream(t.Context(), userID))
	checkpointPage, checkpointErr := r.ReadPage(t.Context(), userID, 1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, checkpointErr)
	require.Empty(t, checkpointPage.RebuildReason)
	appendRead := func(seq int64) {
		require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxReadUpdated, LastReadSeq: seq}}))
	}
	appendRead(1)
	checkpointPage, checkpointErr = r.ReadPage(t.Context(), userID, 1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, checkpointErr)
	require.Empty(t, checkpointPage.RebuildReason)
	require.Equal(t, int64(2), checkpointPage.NextPosition)
	appendRead(2)
	page, err := r.ReadPage(t.Context(), userID, 2, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Len(t, page.Entries, 1)
	require.Equal(t, int64(2), page.Entries[0].LastReadSeq)
	page, err = r.ReadPage(t.Context(), userID, 3, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	for _, invalid := range []int64{-1, sequence.Max + 1} {
		_, err := r.ReadPage(t.Context(), userID, invalid, 10, time.Now().Add(-time.Hour))
		require.Error(t, err)
	}
	_, err = r.Prune(t.Context(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	appendRead(3)
	page, err = r.ReadPage(t.Context(), userID, 3, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Len(t, page.Entries, 1)
	require.Equal(t, int64(4), page.NextPosition)
	require.Equal(t, int64(3), page.Entries[0].LastReadSeq)
	page, err = r.ReadPage(t.Context(), userID, 1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, "expired_position", page.RebuildReason)
	page, err = r.ReadPage(t.Context(), userID, 2, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, "expired_position", page.RebuildReason)
}

func TestSequenceLastSafePositionAndRollback(t *testing.T) {
	db, _, convID, _ := inboxIntegrationDB(t)
	r := &SequenceRepo{}
	require.NoError(t, db.Create(&model.Sequence{ConvID: convID, CurrentSeq: sequence.Max - 1}).Error)
	messageID := storageID(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		seq, err := r.NextSeq(t.Context(), tx, convID)
		if err != nil {
			return err
		}
		require.Equal(t, sequence.Max, seq)
		return tx.Create(&model.Message{ID: messageID, ConvID: convID, Seq: seq}).Error
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := r.NextSeq(t.Context(), tx, convID)
		return err
	}))
	message, err := NewMessageRepo(db).GetByID(t.Context(), messageID)
	require.NoError(t, err)
	require.Equal(t, sequence.Max, message.Seq)
	require.NoError(t, NewMessageRepo(db).DeleteWithTx(t.Context(), db.DB, messageID))
	_, err = NewMessageRepo(db).GetByID(t.Context(), messageID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	// Removing history does not reset the allocator.
	_, err = r.NextSeq(t.Context(), db.DB, convID)
	require.Error(t, err)
}

func TestOutboxStreamOrderingRetriesFailuresAndConcurrentDispatchers(t *testing.T) {
	db, _, firstConv, secondConv := inboxIntegrationDB(t)
	r := NewOutboxRepo(db)
	later := time.Now().Add(time.Hour)
	first := model.OutboxEvent{ID: storageID(t), ConvID: firstConv, PublicationSequence: 1, Topic: "storage-test", Key: firstConv, Payload: model.JSONBytes(`{}`), NextRetryAt: &later, CreatedAt: time.Now().Add(time.Hour)}
	second := model.OutboxEvent{ID: storageID(t), ConvID: firstConv, PublicationSequence: 2, Topic: "storage-test", Key: firstConv, Payload: model.JSONBytes(`{}`), CreatedAt: time.Now().Add(-time.Hour)}
	independent := model.OutboxEvent{ID: storageID(t), ConvID: secondConv, PublicationSequence: 1, Topic: "storage-test", Key: secondConv, Payload: model.JSONBytes(`{}`)}
	for _, event := range []*model.OutboxEvent{&first, &second, &independent} {
		require.NoError(t, r.Insert(t.Context(), db.DB, event))
	}
	fetchedIDs := func(outbox *OutboxRepo) []string {
		events, err := outbox.FetchPending(t.Context(), 10)
		require.NoError(t, err)
		ids := make([]string, len(events))
		for i := range events {
			ids[i] = events[i].ID
		}
		return ids
	}
	require.Equal(t, []string{independent.ID}, fetchedIDs(r))
	require.NoError(t, r.MarkFailed(t.Context(), first.ID, "terminal failure"))
	require.Equal(t, []string{independent.ID}, fetchedIDs(r))
	// Simulate an operator retry of the original predecessor, not a replacement identity.
	require.NoError(t, db.Model(&model.OutboxEvent{}).Where("id = ?", first.ID).Updates(map[string]any{"status": model.OutboxStatusPending, "next_retry_at": nil}).Error)
	// Invert UUID identity order with a timestamp tie: only publication order matters.
	stamp := time.Now()
	if first.ID < second.ID {
		first.ID, second.ID = second.ID, first.ID
		// Reinsert the same logical stream with the inverse allocation identities.
		require.NoError(t, db.Where("conv_id = ?", firstConv).Delete(&model.OutboxEvent{}).Error)
		first.NextRetryAt = nil
		first.CreatedAt, second.CreatedAt = stamp, stamp
		require.NoError(t, r.Insert(t.Context(), db.DB, &first))
		require.NoError(t, r.Insert(t.Context(), db.DB, &second))
	} else {
		require.NoError(t, db.Model(&model.OutboxEvent{}).Where("conv_id = ?", firstConv).Update("created_at", stamp).Error)
	}
	require.Greater(t, first.ID, second.ID)
	// Order the independent stream after the first so a limit of one locks its head only.
	require.NoError(t, db.Model(&model.OutboxEvent{}).Where("id = ?", independent.ID).Update("created_at", stamp.Add(time.Hour)).Error)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	locked := NewOutboxRepo(&database.DB{DB: tx})
	events, err := locked.FetchPending(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, first.ID, events[0].ID)
	// SKIP LOCKED skips that entire ordered stream, not just the predecessor.
	require.Equal(t, []string{independent.ID}, fetchedIDs(r))
	require.NoError(t, r.MarkSent(t.Context(), independent.ID))
	require.NoError(t, locked.MarkSent(t.Context(), first.ID))
	// A sent marker is not visible until the dispatcher's transaction commits.
	require.Empty(t, fetchedIDs(r))
	require.NoError(t, tx.Commit().Error)
	require.Equal(t, []string{second.ID}, fetchedIDs(r))
}

func TestConcurrentAllocatorsStopAtLastSafePosition(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	require.NoError(t, db.Create(&model.Sequence{ConvID: convID, CurrentSeq: sequence.Max - 1}).Error)
	require.NoError(t, db.Create(&model.InboxStream{UserID: userID, Position: sequence.Max - 1, RetainedPosition: sequence.Max - 1}).Error)
	messageIDs := []string{storageID(t), storageID(t)}
	changeIDs := []string{storageID(t), storageID(t)}
	start := make(chan struct{})
	type allocation struct {
		index            int
		seqErr, inboxErr error
	}
	results := make(chan allocation, 2)
	var writers sync.WaitGroup
	for i := range 2 {
		writers.Go(func() {
			<-start
			seqErr := db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
				seq, err := (&SequenceRepo{}).NextSeq(t.Context(), tx, convID)
				if err != nil {
					return err
				}
				return tx.Create(&model.Message{ID: messageIDs[i], ConvID: convID, Seq: seq}).Error
			})
			inboxErr := NewInboxRepo(db).BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: changeIDs[i], Kind: model.InboxConversationUpsert}})
			results <- allocation{i, seqErr, inboxErr}
		})
	}
	close(start)
	writers.Wait()
	close(results)
	seqSuccess, inboxSuccess := 0, 0
	var committedMessage, committedChange string
	for result := range results {
		if result.seqErr == nil {
			seqSuccess++
			committedMessage = messageIDs[result.index]
		}
		if result.inboxErr == nil {
			inboxSuccess++
			committedChange = changeIDs[result.index]
		}
	}
	require.Equal(t, 1, seqSuccess)
	require.Equal(t, 1, inboxSuccess)
	messages, err := NewMessageRepo(db).GetByConvID(t.Context(), convID, 10, 0, 0, 0, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, committedMessage, messages[0].ID)
	require.Equal(t, sequence.Max, messages[0].Seq)
	page, err := NewInboxRepo(db).ReadPage(t.Context(), userID, sequence.Max-1, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Len(t, page.Entries, 1)
	require.Equal(t, committedChange, page.Entries[0].ChangeID)
	require.Equal(t, sequence.Max, page.Entries[0].Position)
}

func TestAccountDeletionFiltersPagesUnreadAndNullablePreviews(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	member := model.ConversationMember{ID: storageID(t), ConvID: convID, UserID: &userID, MemberType: model.MemberTypeUser}
	require.NoError(t, db.Create(&member).Error)
	otherSender := storageID(t)
	messages := []model.Message{
		{ID: storageID(t), ConvID: convID, SenderID: &otherSender, Seq: 1, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "visible"}},
		{ID: storageID(t), ConvID: convID, SenderID: &otherSender, Seq: 2, MsgType: model.MsgTypeText, Content: model.JSONContent{"text": "hidden"}},
		{ID: storageID(t), ConvID: convID, SenderType: "system", Seq: 3, MsgType: model.MsgTypeSystem},
	}
	require.NoError(t, db.Create(&messages).Error)
	unread, err := NewConversationRepo(db).UnreadCount(t.Context(), convID, userID)
	require.NoError(t, err)
	require.Equal(t, int32(3), unread) // A system message without a sender is still unread.
	r := NewMessageRepo(db)
	for _, message := range messages[1:] {
		inserted, err := r.InsertPersonalDeletion(t.Context(), db.DB, userID, convID, message.ID)
		require.NoError(t, err)
		require.True(t, inserted)
	}
	page, err := r.ForUser(userID).GetByConvID(t.Context(), convID, 1, 0, 0, 0, nil)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, messages[0].ID, page[0].ID) // Hidden rows cannot consume the page limit.
	unread, err = NewConversationRepo(db).UnreadCount(t.Context(), convID, userID)
	require.NoError(t, err)
	require.Equal(t, int32(1), unread)
	convs := []model.Conversation{{ID: convID, LastMessageID: &messages[2].ID}}
	require.NoError(t, r.ProjectConversationPreviews(t.Context(), userID, convs))
	require.Equal(t, &messages[0].ID, convs[0].LastMessageID)
	require.Equal(t, "visible", convs[0].LastMessagePreview)
	_, err = r.InsertPersonalDeletion(t.Context(), db.DB, userID, convID, messages[0].ID)
	require.NoError(t, err)
	require.NoError(t, r.ProjectConversationPreviews(t.Context(), userID, convs))
	require.Nil(t, convs[0].LastMessageID)
	require.Empty(t, convs[0].LastMessagePreview)
	unread, err = NewConversationRepo(db).UnreadCount(t.Context(), convID, userID)
	require.NoError(t, err)
	require.Zero(t, unread)
}

func TestInboxRejectsUnallocatedHoleDespiteAppliedChanges(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	require.NoError(t, r.EnsureStream(t.Context(), userID))
	for _, seq := range []int64{1, 2} {
		require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxReadUpdated, LastReadSeq: seq}}))
	}
	// The existing allocator seam constructs an in-range position never assigned.
	require.NoError(t, db.Model(&model.InboxStream{}).Where("user_id = ?", userID).Update("position", 5).Error)
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxConversationUpsert}}))
	// A stale applied read receives no new position and cannot establish a gap.
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxReadUpdated, LastReadSeq: 1}}))
	page, err := r.ReadPage(t.Context(), userID, 4, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, "unknown_position", page.RebuildReason)
	page, err = r.ReadPage(t.Context(), userID, 2, 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Equal(t, int64(6), page.NextPosition)
	require.Len(t, page.Entries, 2)
	require.Equal(t, int64(2), page.Entries[0].LastReadSeq)
}

func TestCoalescedCheckpointExpiresBeforeSurvivingReadEntry(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewInboxRepo(db)
	require.NoError(t, r.EnsureStream(t.Context(), userID))
	oldChangeID := storageID(t)
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: oldChangeID, Kind: model.InboxReadUpdated, LastReadSeq: 1}}))
	require.NoError(t, r.BatchInsert(t.Context(), []model.UserInbox{{UserID: userID, ConvID: convID, ChangeID: storageID(t), Kind: model.InboxReadUpdated, LastReadSeq: 2}}))
	cutoff := time.Now().Add(-time.Hour)
	require.NoError(t, db.Model(&model.InboxAppliedChange{}).Where("user_id = ? AND change_id = ?", userID, oldChangeID).Update("created_at", cutoff.Add(-time.Hour)).Error)
	// Expiration uses allocation provenance even if that old entry was coalesced.
	page, err := r.ReadPage(t.Context(), userID, 1, 10, cutoff)
	require.NoError(t, err)
	require.Equal(t, "expired_position", page.RebuildReason)
	removed, err := r.Prune(t.Context(), cutoff)
	require.NoError(t, err)
	require.Zero(t, removed)
	page, err = r.ReadPage(t.Context(), userID, 2, 10, cutoff)
	require.NoError(t, err)
	require.Empty(t, page.RebuildReason)
	require.Len(t, page.Entries, 1)
	require.Equal(t, int64(2), page.Entries[0].LastReadSeq)
}

func TestTypedMembershipAdmissionAndSendAuthority(t *testing.T) {
	db, userID, convID, _ := inboxIntegrationDB(t)
	r := NewConversationRepo(db)
	botID, sharedID := storageID(t), storageID(t)
	members := []model.ConversationMember{
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeUser, UserID: &userID},
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeBot, BotID: &botID, IsMuted: true},
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeUser, UserID: &sharedID},
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeBot, BotID: &sharedID},
	}
	added, failed, err := r.AddMembersWithinLimit(t.Context(), convID, members, 4)
	require.NoError(t, err)
	require.Equal(t, []string{userID, botID, sharedID, sharedID}, added)
	require.Empty(t, failed)
	added, failed, err = r.AddMembersWithinLimit(t.Context(), convID, []model.ConversationMember{members[1]}, 4)
	require.NoError(t, err)
	require.Empty(t, added)
	require.Equal(t, []string{botID}, failed)
	for _, actor := range []struct {
		id, kind      string
		member, muted bool
	}{
		{userID, model.MemberTypeUser, true, false},
		{userID, model.MemberTypeBot, false, false},
		{botID, model.MemberTypeBot, true, true},
		{botID, model.MemberTypeUser, false, false},
	} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			permission, err := r.CheckSendPermission(t.Context(), tx, convID, actor.id, actor.kind)
			if err != nil {
				return err
			}
			require.Equal(t, actor.member, permission.IsMember)
			require.Equal(t, actor.muted, permission.IsMuted)
			return nil
		}))
	}
	newID := storageID(t)
	_, _, err = r.AddMembersWithinLimit(t.Context(), convID, []model.ConversationMember{{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeBot, BotID: &newID}}, 4)
	require.ErrorIs(t, err, ErrMemberLimitReached)
	// Mixed actor references cannot partially admit an earlier valid candidate.
	_, _, err = r.AddMembersWithinLimit(t.Context(), convID, []model.ConversationMember{
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeUser, UserID: &newID},
		{ID: storageID(t), ConvID: convID, MemberType: model.MemberTypeBot, BotID: &newID, UserID: &newID},
	}, 6)
	require.Error(t, err)
	count, err := r.CountMembers(t.Context(), convID)
	require.NoError(t, err)
	require.Equal(t, int64(4), count)
	_, err = r.GetMember(t.Context(), convID, newID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
