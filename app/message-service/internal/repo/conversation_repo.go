package repo

import (
	"context"
	"errors"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConversationStore is the conversation aggregate's persistence contract. It is
// implemented by ConversationRepo and consumed by the conversation logic; the
// message hot path uses the same type directly in-process.
type ConversationStore interface {
	CreateConversation(ctx context.Context, conv *model.Conversation) error
	GetConversation(ctx context.Context, id int64) (*model.Conversation, error)
	ListConversationsByUser(ctx context.Context, userID int64, cursor int64, limit int, typ *int32) ([]model.Conversation, error)
	ListConversationsByUserPinned(ctx context.Context, userID int64, pinned bool) ([]model.Conversation, error)
	UpdateConversation(ctx context.Context, conv *model.Conversation) error
	UpdateConversationAnnouncement(ctx context.Context, id int64, announcement string) error
	UpdateConversationMutedAll(ctx context.Context, id int64, mutedAll bool) error
	UpdateConversationOwner(ctx context.Context, id int64, newOwnerID int64) error
	DeleteConversation(ctx context.Context, id int64) error
	TouchLastMessage(ctx context.Context, tx *gorm.DB, convID, msgID, seq int64, preview string) error
	CheckSendPermission(ctx context.Context, tx *gorm.DB, convID, userID int64) (*SendPermission, error)
	IncrementMemberCount(ctx context.Context, id int64, delta int) error
	AddMember(ctx context.Context, member *model.ConversationMember) error
	AddMembersBatch(ctx context.Context, members []model.ConversationMember) error
	RemoveMember(ctx context.Context, convID, userID int64) error
	GetMember(ctx context.Context, convID, userID int64) (*model.ConversationMember, error)
	GetMembers(ctx context.Context, convID int64, offset, limit int) ([]model.ConversationMember, error)
	CountMembers(ctx context.Context, convID int64) (int64, error)
	UpdateMember(ctx context.Context, member *model.ConversationMember) error
	UpdateMemberRole(ctx context.Context, convID, userID int64, role int32) error
	IsMember(ctx context.Context, convID, userID int64) (bool, error)
	MuteMember(ctx context.Context, convID, userID int64, muteUntil int64) error
	UnmuteMember(ctx context.Context, convID, userID int64) error
	UpsertReadSeq(ctx context.Context, convID, userID int64, seq int64, id int64) (int64, error)
	UnreadCounts(ctx context.Context, convID int64, userIDs []int64) (map[int64]int32, error)
	UnreadCountsByUser(ctx context.Context, userID int64, convIDs []int64) (map[int64]int32, error)
	UnreadCount(ctx context.Context, convID, userID int64) (int32, error)
	MemberIDs(ctx context.Context, convID int64) ([]int64, error)
	ListIDsByUser(ctx context.Context, userID int64) ([]int64, error)
	GetReadSeqs(ctx context.Context, convID int64) ([]model.ConvReadSeq, error)
	GetReadSeq(ctx context.Context, convID, userID int64) (*model.ConvReadSeq, error)
	GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error)
	GetSettings(ctx context.Context, convID, userID int64) (*model.ConvSettings, error)
	UpsertSettings(ctx context.Context, s *model.ConvSettings) error

	FindPrivateConv(ctx context.Context, userID1, userID2 int64) (*model.Conversation, error)

	GetBot(ctx context.Context, botID int64) (*model.Bot, error)
	AddBot(ctx context.Context, bot *model.ConvBot) error
	RemoveBot(ctx context.Context, convID, botID int64) error
	UpdateBot(ctx context.Context, convID, botID int64, settings any) error
	ListBotsByConv(ctx context.Context, convID int64) ([]model.ConvBot, error)
	GetBotInConv(ctx context.Context, convID, botID int64) (*model.ConvBot, error)
	AddBotWithMember(ctx context.Context, bot *model.ConvBot, member *model.ConversationMember) error
	RemoveBotWithMember(ctx context.Context, convID, botID int64) error
	GetBotsByIDs(ctx context.Context, ids []int64) ([]model.Bot, error)
}

type ConversationRepo struct {
	DB *database.DB
}

func NewConversationRepo(db *database.DB) *ConversationRepo {
	return &ConversationRepo{DB: db}
}

// ========== Conversation CRUD ==========

func (r *ConversationRepo) CreateConversation(ctx context.Context, conv *model.Conversation) error {
	return r.DB.WithContext(ctx).Create(conv).Error
}

func (r *ConversationRepo) GetConversation(ctx context.Context, id int64) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepo) ListConversationsByUser(ctx context.Context, userID int64, cursor int64, limit int, typ *int32) ([]model.Conversation, error) {
	var convs []model.Conversation
	q := r.DB.WithContext(ctx).
		Table("conversations c").
		Joins("INNER JOIN conv_members cm ON cm.conv_id = c.id AND cm.user_id = ?", userID)
	if cursor > 0 {
		q = q.Where("c.updated_at < (SELECT updated_at FROM conversations WHERE id = ?)", cursor)
	}
	if typ != nil {
		q = q.Where("c.type = ?", *typ)
	}
	err := q.Order("c.updated_at DESC").Limit(limit).Find(&convs).Error
	return convs, err
}

func (r *ConversationRepo) ListConversationsByUserPinned(ctx context.Context, userID int64, pinned bool) ([]model.Conversation, error) {
	var convs []model.Conversation
	err := r.DB.WithContext(ctx).
		Table("conversations c").
		Joins("INNER JOIN conv_settings cs ON cs.conv_id = c.id AND cs.user_id = ? AND cs.is_pinned = ?", userID, pinned).
		Order("c.updated_at DESC").Find(&convs).Error
	return convs, err
}

func (r *ConversationRepo) UpdateConversation(ctx context.Context, conv *model.Conversation) error {
	return r.DB.WithContext(ctx).Model(conv).Where("id = ?", conv.ID).
		Updates(map[string]interface{}{
			"name":       conv.Name,
			"avatar":     conv.Avatar,
			"background": conv.Background,
			"updated_at": time.Now(),
		}).Error
}

func (r *ConversationRepo) UpdateConversationAnnouncement(ctx context.Context, id int64, announcement string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"announcement": announcement, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) UpdateConversationMutedAll(ctx context.Context, id int64, mutedAll bool) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"is_muted_all": mutedAll, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) UpdateConversationOwner(ctx context.Context, id int64, newOwnerID int64) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"owner_id": newOwnerID, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) DeleteConversation(ctx context.Context, id int64) error {
	return r.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.Conversation{}).Error
}

func (r *ConversationRepo) IncrementMemberCount(ctx context.Context, id int64, delta int) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Update("member_count", gorm.Expr("member_count + ?", delta)).Error
}

// SendPermission is everything the send path must know before writing a message.
type SendPermission struct {
	IsMember         bool
	IsMuted          bool
	IsMutedAll       bool
	MuteUntil        int64
	ConversationType int32
	OtherMemberIDs   []int64
}

// CheckSendPermission locks the conversation before its membership and permission
// rows. The caller must hold this transaction through sequence allocation and
// message/outbox/latest-message writes so removal and mute changes cannot race it.
func (r *ConversationRepo) CheckSendPermission(ctx context.Context, tx *gorm.DB, convID, userID int64) (*SendPermission, error) {
	var conv model.Conversation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", convID).Take(&conv).Error; err != nil {
		return nil, err
	}
	perm := &SendPermission{
		ConversationType: conv.Type,
		IsMutedAll:       conv.IsMutedAll,
	}

	var member model.ConversationMember
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return perm, nil
	}
	if err != nil {
		return nil, err
	}
	perm.IsMember = true
	perm.IsMuted = member.IsMuted
	perm.MuteUntil = member.MuteUntil

	if conv.Type == model.ConvTypePrivate {
		var members []model.ConversationMember
		if err := tx.WithContext(ctx).Select("user_id").Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("conv_id = ? AND user_id <> ?", convID, userID).Order("user_id").Find(&members).Error; err != nil {
			return nil, err
		}
		for _, member := range members {
			perm.OtherMemberIDs = append(perm.OtherMemberIDs, member.UserID)
		}
	}
	return perm, nil
}

// TouchLastMessage records the newest message in a conversation inside the
// caller's transaction. This is the single write path for last_message / max_seq
// / updated_at: the conversation list orders by updated_at, so the three move
// together with the message insert.
func (r *ConversationRepo) TouchLastMessage(ctx context.Context, tx *gorm.DB, convID, msgID, seq int64, preview string) error {
	result := tx.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", convID).
		Updates(map[string]any{
			"last_message_id":      msgID,
			"last_message_preview": preview,
			"max_seq":              seq,
			"updated_at":           time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ========== Members ==========

func (r *ConversationRepo) AddMember(ctx context.Context, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Create(member).Error
}

func (r *ConversationRepo) AddMembersBatch(ctx context.Context, members []model.ConversationMember) error {
	if len(members) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Create(&members).Error
}

func (r *ConversationRepo) RemoveMember(ctx context.Context, convID, userID int64) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).
		Delete(&model.ConversationMember{}).Error
}

func (r *ConversationRepo) GetMember(ctx context.Context, convID, userID int64) (*model.ConversationMember, error) {
	var m model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *ConversationRepo) GetMembers(ctx context.Context, convID int64, offset, limit int) ([]model.ConversationMember, error) {
	var members []model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).
		Order("role ASC, joined_at ASC").Offset(offset).Limit(limit).Find(&members).Error
	return members, err
}

func (r *ConversationRepo) CountMembers(ctx context.Context, convID int64) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).Where("conv_id = ?", convID).Count(&count).Error
	return count, err
}

func (r *ConversationRepo) UpdateMember(ctx context.Context, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Model(member).Where("conv_id = ? AND user_id = ?", member.ConvID, member.UserID).
		Updates(map[string]interface{}{"role": member.Role, "alias": member.Alias}).Error
}

func (r *ConversationRepo) UpdateMemberRole(ctx context.Context, convID, userID int64, role int32) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Update("role", role).Error
}

func (r *ConversationRepo) IsMember(ctx context.Context, convID, userID int64) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Count(&count).Error
	return count > 0, err
}

// ========== Mute ==========

func (r *ConversationRepo) MuteMember(ctx context.Context, convID, userID int64, muteUntil int64) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": true, "mute_until": muteUntil}).Error
}

func (r *ConversationRepo) UnmuteMember(ctx context.Context, convID, userID int64) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": false, "mute_until": 0}).Error
}

// ========== Read Status ==========

// UpsertReadSeq advances a member's read position. The position never regresses
// (a stale client cannot un-read) and never exceeds the conversation's max_seq.
// It returns the persisted position, including for stale or out-of-range requests.
func (r *ConversationRepo) UpsertReadSeq(ctx context.Context, convID, userID int64, seq int64, id int64) (int64, error) {
	var stored struct {
		LastReadSeq int64 `gorm:"column:last_read_seq"`
	}
	result := r.DB.WithContext(ctx).Raw(`
		WITH member AS (
			SELECT c.id AS conv_id, m.user_id, c.max_seq
			FROM conversations c
			JOIN conv_members m ON m.conv_id = c.id
			WHERE c.id = ? AND m.user_id = ?
			FOR SHARE OF c, m
		)
		INSERT INTO conv_read_seqs (id, conv_id, user_id, last_read_seq, read_at)
		SELECT ?, conv_id, user_id, LEAST(GREATEST(?, 0), max_seq), NOW()
		FROM member
		WHERE TRUE
		ON CONFLICT (conv_id, user_id) DO UPDATE
		SET last_read_seq = GREATEST(conv_read_seqs.last_read_seq, EXCLUDED.last_read_seq),
		    read_at = CASE WHEN conv_read_seqs.last_read_seq < EXCLUDED.last_read_seq
		                   THEN EXCLUDED.read_at ELSE conv_read_seqs.read_at END
		RETURNING last_read_seq`, convID, userID, id, seq).Scan(&stored)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return stored.LastReadSeq, nil
}

// UnreadCounts returns, for each requested member of the conversation, how many
// messages arrived after that member's read position and were not sent by the
// member. It is the single definition of "unread" in the domain: the
// conversation list, GetConversation, read receipts and push envelopes all use
// it. Users that are not members of the conversation are absent from the result.
func (r *ConversationRepo) UnreadCounts(ctx context.Context, convID int64, userIDs []int64) (map[int64]int32, error) {
	counts := make(map[int64]int32, len(userIDs))
	if len(userIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		UserID int64 `gorm:"column:user_id"`
		Count  int32 `gorm:"column:unread"`
	}
	err := r.DB.WithContext(ctx).Raw(`
		SELECT m.user_id AS user_id, COUNT(msg.id)::int AS unread
		FROM conv_members m
		LEFT JOIN conv_read_seqs r ON r.conv_id = m.conv_id AND r.user_id = m.user_id
		LEFT JOIN messages msg ON msg.conv_id = m.conv_id
		     AND msg.seq > COALESCE(r.last_read_seq, 0)
		     AND msg.sender_id <> m.user_id
		WHERE m.conv_id = ? AND m.user_id IN ?
		GROUP BY m.user_id`, convID, userIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.UserID] = row.Count
	}
	return counts, nil
}

// UnreadCountsByUser returns the unread count of one user for each of the given
// conversations, in a single query, for the conversation list.
func (r *ConversationRepo) UnreadCountsByUser(ctx context.Context, userID int64, convIDs []int64) (map[int64]int32, error) {
	counts := make(map[int64]int32, len(convIDs))
	if len(convIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		ConvID int64 `gorm:"column:conv_id"`
		Count  int32 `gorm:"column:unread"`
	}
	err := r.DB.WithContext(ctx).Raw(`
		SELECT m.conv_id AS conv_id, COUNT(msg.id)::int AS unread
		FROM conv_members m
		LEFT JOIN conv_read_seqs r ON r.conv_id = m.conv_id AND r.user_id = m.user_id
		LEFT JOIN messages msg ON msg.conv_id = m.conv_id
		     AND msg.seq > COALESCE(r.last_read_seq, 0)
		     AND msg.sender_id <> m.user_id
		WHERE m.user_id = ? AND m.conv_id IN ?
		GROUP BY m.conv_id`, userID, convIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ConvID] = row.Count
	}
	return counts, nil
}

// UnreadCount is UnreadCounts for a single user.
func (r *ConversationRepo) UnreadCount(ctx context.Context, convID, userID int64) (int32, error) {
	counts, err := r.UnreadCounts(ctx, convID, []int64{userID})
	if err != nil {
		return 0, err
	}
	return counts[userID], nil
}

// MemberIDs returns every member of a conversation, bots included.
func (r *ConversationRepo) MemberIDs(ctx context.Context, convID int64) ([]int64, error) {
	var ids []int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ?", convID).Pluck("user_id", &ids).Error
	return ids, err
}

// ListIDsByUser returns the conversations a user belongs to.
func (r *ConversationRepo) ListIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	var ids []int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("user_id = ?", userID).Pluck("conv_id", &ids).Error
	return ids, err
}

func (r *ConversationRepo) GetReadSeqs(ctx context.Context, convID int64) ([]model.ConvReadSeq, error) {
	var seqs []model.ConvReadSeq
	err := r.DB.WithContext(ctx).Model(&model.ConvReadSeq{}).
		Select("conv_read_seqs.*").
		Joins("JOIN conv_members m ON m.conv_id = conv_read_seqs.conv_id AND m.user_id = conv_read_seqs.user_id").
		Where("conv_read_seqs.conv_id = ?", convID).Find(&seqs).Error
	return seqs, err
}

func (r *ConversationRepo) GetReadSeq(ctx context.Context, convID, userID int64) (*model.ConvReadSeq, error) {
	var rs model.ConvReadSeq
	err := r.DB.WithContext(ctx).Model(&model.ConvReadSeq{}).
		Select("conv_read_seqs.*").
		Joins("JOIN conv_members m ON m.conv_id = conv_read_seqs.conv_id AND m.user_id = conv_read_seqs.user_id").
		Where("conv_read_seqs.conv_id = ? AND conv_read_seqs.user_id = ?", convID, userID).First(&rs).Error
	if err != nil {
		return nil, err
	}
	return &rs, nil
}

func (r *ConversationRepo) GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error) {
	if len(convIDs) == 0 {
		return nil, nil
	}
	var rows []struct {
		ConvID      int64 `gorm:"column:conv_id"`
		LastReadSeq int64 `gorm:"column:last_read_seq"`
	}
	err := r.DB.WithContext(ctx).
		Model(&model.ConvReadSeq{}).
		Select("conv_read_seqs.conv_id, conv_read_seqs.last_read_seq").
		Joins("JOIN conv_members m ON m.conv_id = conv_read_seqs.conv_id AND m.user_id = conv_read_seqs.user_id").
		Where("conv_read_seqs.conv_id IN ? AND conv_read_seqs.user_id = ?", convIDs, userID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, row := range rows {
		m[row.ConvID] = row.LastReadSeq
	}
	return m, nil
}

// ========== Settings ==========

func (r *ConversationRepo) GetSettings(ctx context.Context, convID, userID int64) (*model.ConvSettings, error) {
	var s model.ConvSettings
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ConversationRepo) UpsertSettings(ctx context.Context, s *model.ConvSettings) error {
	return r.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "conv_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"is_muted", "is_pinned"}),
		}).
		Create(s).Error
}

// ========== Private Conversation ==========

func (r *ConversationRepo) FindPrivateConv(ctx context.Context, userID1, userID2 int64) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.DB.WithContext(ctx).
		Table("conversations c").
		Joins("INNER JOIN conv_members m1 ON m1.conv_id = c.id AND m1.user_id = ?", userID1).
		Joins("INNER JOIN conv_members m2 ON m2.conv_id = c.id AND m2.user_id = ?", userID2).
		Where("c.type = ?", 1). // PRIVATE
		First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// ========== Bot Management ==========

// GetBot reads the bot directory. The bot domain owns this table; the message
// domain only reads the two display columns it needs (ADR-0007).
func (r *ConversationRepo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	var bot model.Bot
	err := r.DB.WithContext(ctx).Select("id, name, avatar").Where("id = ?", botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *ConversationRepo) AddBot(ctx context.Context, bot *model.ConvBot) error {
	return r.DB.WithContext(ctx).Create(bot).Error
}

func (r *ConversationRepo) RemoveBot(ctx context.Context, convID, botID int64) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).
		Delete(&model.ConvBot{}).Error
}

func (r *ConversationRepo) UpdateBot(ctx context.Context, convID, botID int64, settings any) error {
	updates := map[string]any{}
	if settings != nil {
		updates["bot_settings"] = settings
	}
	if len(updates) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&model.ConvBot{}).
		Where("conv_id = ? AND bot_id = ?", convID, botID).
		Updates(updates).Error
}

func (r *ConversationRepo) ListBotsByConv(ctx context.Context, convID int64) ([]model.ConvBot, error) {
	var bots []model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).Find(&bots).Error
	return bots, err
}

func (r *ConversationRepo) GetBotInConv(ctx context.Context, convID, botID int64) (*model.ConvBot, error) {
	var bot model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

// ========== Bot with Member (transactional) ==========

func (r *ConversationRepo) AddBotWithMember(ctx context.Context, bot *model.ConvBot, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(bot).Error; err != nil {
			return err
		}
		return tx.Create(member).Error
	})
}

func (r *ConversationRepo) RemoveBotWithMember(ctx context.Context, convID, botID int64) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("conv_id = ? AND bot_id = ?", convID, botID).Delete(&model.ConvBot{}).Error; err != nil {
			return err
		}
		return tx.Where("conv_id = ? AND user_id = ?", convID, botID).Delete(&model.ConversationMember{}).Error
	})
}

func (r *ConversationRepo) GetBotsByIDs(ctx context.Context, ids []int64) ([]model.Bot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var bots []model.Bot
	err := r.DB.WithContext(ctx).Select("id, name, avatar").Where("id IN ?", ids).Find(&bots).Error
	return bots, err
}

// ========== Transaction ==========

func (r *ConversationRepo) BeginTx(ctx context.Context) (*gorm.DB, error) {
	return r.DB.WithContext(ctx).Begin(), nil
}
