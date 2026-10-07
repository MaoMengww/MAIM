package repo

import (
	"context"
	"errors"
	"time"

	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMemberLimitReached = errors.New("conversation member limit reached")

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

func (r *ConversationRepo) GetConversation(ctx context.Context, id string) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepo) ListConversationsByUser(ctx context.Context, userID string, cursor string, limit int, typ *int32, pinnedFirst ...bool) ([]model.Conversation, error) {
	var convs []model.Conversation
	q := r.DB.WithContext(ctx).Table("conversations c").Select("c.*").
		Joins("INNER JOIN conv_members cm ON cm.conv_id = c.id AND cm.user_id = ?", userID)
	pinned := len(pinnedFirst) != 0 && pinnedFirst[0]
	if pinned {
		q = q.Joins("LEFT JOIN conv_settings cs ON cs.conv_id = c.id AND cs.user_id = ?", userID)
		if cursor != "" {
			q = q.Where(`(COALESCE(cs.is_pinned, false), c.updated_at, c.id) <
				(SELECT COALESCE(s.is_pinned, false), anchor.updated_at, anchor.id
				 FROM conversations anchor LEFT JOIN conv_settings s ON s.conv_id = anchor.id AND s.user_id = ? WHERE anchor.id = ?)`, userID, cursor)
		}
		q = q.Order("COALESCE(cs.is_pinned, false) DESC")
	} else if cursor != "" {
		q = q.Where("(c.updated_at, c.id) < (SELECT updated_at, id FROM conversations WHERE id = ?)", cursor)
	}
	if typ != nil {
		q = q.Where("c.type = ?", *typ)
	}
	err := q.Order("c.updated_at DESC, c.id DESC").Limit(limit).Find(&convs).Error
	return convs, err
}

func (r *ConversationRepo) ListConversationsByUserPinned(ctx context.Context, userID string, pinned bool) ([]model.Conversation, error) {
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

func (r *ConversationRepo) UpdateConversationAnnouncement(ctx context.Context, id string, announcement string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"announcement": announcement, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) UpdateConversationMutedAll(ctx context.Context, id string, mutedAll bool) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"is_muted_all": mutedAll, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) UpdateConversationOwner(ctx context.Context, id string, newOwnerID string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"owner_id": newOwnerID, "updated_at": time.Now()}).Error
}

func (r *ConversationRepo) DeleteConversation(ctx context.Context, id string) error {
	return r.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.Conversation{}).Error
}

func (r *ConversationRepo) IncrementMemberCount(ctx context.Context, id string, delta int) error {
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
	OtherMemberIDs   []string
}

// CheckSendPermission locks the conversation before its membership and permission
// rows. The caller must hold this transaction through sequence allocation and
// message/outbox/latest-message writes so removal and mute changes cannot race it.
func (r *ConversationRepo) CheckSendPermission(ctx context.Context, tx *gorm.DB, convID, actorID, memberType string) (*SendPermission, error) {
	if err := identity.Validate(actorID); err != nil {
		return nil, err
	}
	memberColumn := "user_id"
	switch memberType {
	case model.MemberTypeUser:
	case model.MemberTypeBot:
		memberColumn = "bot_id"
	default:
		return nil, errors.New("unsupported conversation member type")
	}
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
		Where("conv_id = ? AND member_type = ? AND "+memberColumn+" = ?", convID, memberType, actorID).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return perm, nil
	}
	if err != nil {
		return nil, err
	}
	perm.IsMember = true
	perm.IsMuted = member.IsMuted
	perm.MuteUntil = member.MuteUntil

	if conv.Type == model.ConvTypePrivate && memberType == model.MemberTypeUser {
		var members []model.ConversationMember
		if err := tx.WithContext(ctx).Select("user_id").Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("conv_id = ? AND member_type = ? AND user_id <> ?", convID, model.MemberTypeUser, actorID).Order("user_id").Find(&members).Error; err != nil {
			return nil, err
		}
		for _, member := range members {
			if member.UserID != nil {
				perm.OtherMemberIDs = append(perm.OtherMemberIDs, *member.UserID)
			}
		}
	}
	return perm, nil
}

// TouchLastMessage records the newest message in a conversation inside the
// caller's transaction. This is the single write path for last_message / max_seq
// / updated_at: the conversation list orders by updated_at, so the three move
// together with the message insert.
func (r *ConversationRepo) TouchLastMessage(ctx context.Context, tx *gorm.DB, convID, msgID string, seq int64, preview string) error {
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

// AddMembersWithinLimit serializes additions on the conversation row and commits
// the entire new-member batch together with its member count. Existing members,
// including bots, count toward capacity; repeated IDs do not consume new slots.
// The candidate slice is compacted in place.
func (r *ConversationRepo) AddMembersWithinLimit(ctx context.Context, convID string, members []model.ConversationMember, maxMembers int) (added, failed []string, err error) {
	if len(members) == 0 {
		return nil, nil, nil
	}
	err = r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", convID).Take(&conv).Error; err != nil {
			return err
		}

		// Read the membership rows, not the denormalized count: other existing
		// membership paths can leave that count behind (notably bot additions).
		var currentMembers []model.ConversationMember
		if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ?", convID).Find(&currentMembers).Error; err != nil {
			return err
		}
		type memberKey struct{ kind, id string }
		keyFor := func(member model.ConversationMember) (memberKey, error) {
			var id *string
			switch member.MemberType {
			case model.MemberTypeUser:
				if member.BotID != nil {
					return memberKey{}, errors.New("user member cannot reference a bot")
				}
				id = member.UserID
			case model.MemberTypeBot:
				if member.UserID != nil {
					return memberKey{}, errors.New("bot member cannot reference a user")
				}
				id = member.BotID
			default:
				return memberKey{}, errors.New("unsupported conversation member type")
			}
			if id == nil {
				return memberKey{}, errors.New("member requires its actor identity")
			}
			if err := identity.Validate(*id); err != nil {
				return memberKey{}, err
			}
			return memberKey{member.MemberType, *id}, nil
		}
		seen := make(map[memberKey]struct{}, len(currentMembers))
		for _, current := range currentMembers {
			key, err := keyFor(current)
			if err != nil {
				return err
			}
			seen[key] = struct{}{}
		}
		pending := members[:0]
		for _, member := range members {
			if member.ConvID != convID {
				return errors.New("member belongs to another conversation")
			}
			key, err := keyFor(member)
			if err != nil {
				return err
			}
			if _, exists := seen[key]; exists {
				failed = append(failed, key.id)
				continue
			}
			seen[key] = struct{}{}
			pending = append(pending, member)
			added = append(added, key.id)
		}
		if len(pending) == 0 {
			return nil
		}
		if len(currentMembers)+len(pending) > maxMembers {
			return ErrMemberLimitReached
		}
		if err := tx.Create(&pending).Error; err != nil {
			return err
		}
		return tx.Model(&model.Conversation{}).Where("id = ?", convID).
			Update("member_count", gorm.Expr("member_count + ?", len(pending))).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return added, failed, nil
}

func (r *ConversationRepo) RemoveMember(ctx context.Context, convID, userID string) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).
		Delete(&model.ConversationMember{}).Error
}

func (r *ConversationRepo) GetMember(ctx context.Context, convID, userID string) (*model.ConversationMember, error) {
	var m model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND member_type = ? AND user_id = ?", convID, model.MemberTypeUser, userID).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *ConversationRepo) GetBotMember(ctx context.Context, convID, botID string) (*model.ConversationMember, error) {
	var member model.ConversationMember
	if err := r.DB.WithContext(ctx).Where("conv_id = ? AND member_type = ? AND bot_id = ?", convID, model.MemberTypeBot, botID).Take(&member).Error; err != nil {
		return nil, err
	}
	return &member, nil
}

func (r *ConversationRepo) GetMembers(ctx context.Context, convID string, offset, limit int) ([]model.ConversationMember, error) {
	var members []model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).
		Order("role ASC, joined_at ASC").Offset(offset).Limit(limit).Find(&members).Error
	return members, err
}

func (r *ConversationRepo) CountMembers(ctx context.Context, convID string) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).Where("conv_id = ?", convID).Count(&count).Error
	return count, err
}

func (r *ConversationRepo) UpdateMember(ctx context.Context, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Model(member).Where("conv_id = ? AND user_id = ?", member.ConvID, member.UserID).
		Updates(map[string]interface{}{"role": member.Role, "alias": member.Alias}).Error
}

func (r *ConversationRepo) UpdateMemberRole(ctx context.Context, convID, userID string, role int32) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Update("role", role).Error
}

func (r *ConversationRepo) IsMember(ctx context.Context, convID, userID string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Count(&count).Error
	return count > 0, err
}

// ========== Mute ==========

func (r *ConversationRepo) MuteMember(ctx context.Context, convID, userID string, muteUntil int64) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": true, "mute_until": muteUntil}).Error
}

func (r *ConversationRepo) UnmuteMember(ctx context.Context, convID, userID string) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": false, "mute_until": 0}).Error
}

// ========== Read Status ==========

// UpsertReadSeq advances a member's read position in the caller's transaction.
// The conversation lock serializes changes with outbox publication; membership
// stays valid until commit, and the position never regresses or exceeds max_seq.
// Only an actual advance changes read_at and requires an inbox change.
func (r *ConversationRepo) UpsertReadSeq(ctx context.Context, tx *gorm.DB, convID, userID string, seq int64, id string) (lastReadSeq int64, advanced bool, err error) {
	if err := sequence.Validate(seq); err != nil {
		return 0, false, err
	}
	var conv struct {
		MaxSeq int64 `gorm:"column:max_seq"`
	}
	db := tx.WithContext(ctx)
	locked := db.Raw(`
		SELECT c.max_seq
		FROM conversations c
		JOIN conv_members m ON m.conv_id = c.id
		WHERE c.id = ? AND m.user_id = ?
		FOR UPDATE OF c FOR SHARE OF m`, convID, userID).Scan(&conv)
	if locked.Error != nil {
		return 0, false, locked.Error
	}
	if locked.RowsAffected == 0 {
		return 0, false, gorm.ErrRecordNotFound
	}
	seq = min(max(seq, 0), conv.MaxSeq)
	var stored struct {
		LastReadSeq int64 `gorm:"column:last_read_seq"`
	}
	result := db.Raw(`
		INSERT INTO conv_read_seqs (id, conv_id, user_id, last_read_seq, read_at)
		VALUES (?, ?, ?, ?, NOW())
		ON CONFLICT (conv_id, user_id) DO UPDATE
		SET last_read_seq = EXCLUDED.last_read_seq, read_at = EXCLUDED.read_at
		WHERE conv_read_seqs.last_read_seq < EXCLUDED.last_read_seq
		RETURNING last_read_seq`, id, convID, userID, seq).Scan(&stored)
	if result.Error != nil {
		return 0, false, result.Error
	}
	if result.RowsAffected != 0 {
		return stored.LastReadSeq, stored.LastReadSeq > 0, nil
	}
	// A stale request did not mutate the row. Read the same persisted input used
	// by conversation lists, details and receipts, without emitting a new change.
	err = db.Model(&model.ConvReadSeq{}).
		Select("last_read_seq").Where("conv_id = ? AND user_id = ?", convID, userID).
		Scan(&stored).Error
	return stored.LastReadSeq, false, err
}

// UnreadCounts returns, for each requested member of the conversation, how many
// messages arrived after that member's read position and were not sent by the
// member. It is the single definition of "unread" in the domain: the
// conversation list, GetConversation, read receipts and push envelopes all use
// it. Users that are not members of the conversation are absent from the result.
func (r *ConversationRepo) UnreadCounts(ctx context.Context, convID string, userIDs []string) (map[string]int32, error) {
	counts := make(map[string]int32, len(userIDs))
	if len(userIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		UserID string `gorm:"column:user_id"`
		Count  int32  `gorm:"column:unread"`
	}
	err := r.DB.WithContext(ctx).Raw(`
		SELECT m.user_id AS user_id, COUNT(msg.id)::int AS unread
		FROM conv_members m
		LEFT JOIN conv_read_seqs r ON r.conv_id = m.conv_id AND r.user_id = m.user_id
		LEFT JOIN messages msg ON msg.conv_id = m.conv_id
		     AND msg.seq > COALESCE(r.last_read_seq, 0)
		     AND msg.sender_id IS DISTINCT FROM m.user_id
       AND NOT EXISTS (SELECT 1 FROM personal_message_deletions d
         WHERE d.user_id = m.user_id AND d.conv_id = msg.conv_id AND d.message_id = msg.id)
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
func (r *ConversationRepo) UnreadCountsByUser(ctx context.Context, userID string, convIDs []string) (map[string]int32, error) {
	counts := make(map[string]int32, len(convIDs))
	if len(convIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		ConvID string `gorm:"column:conv_id"`
		Count  int32  `gorm:"column:unread"`
	}
	err := r.DB.WithContext(ctx).Raw(`
		SELECT m.conv_id AS conv_id, COUNT(msg.id)::int AS unread
		FROM conv_members m
		LEFT JOIN conv_read_seqs r ON r.conv_id = m.conv_id AND r.user_id = m.user_id
		LEFT JOIN messages msg ON msg.conv_id = m.conv_id
		     AND msg.seq > COALESCE(r.last_read_seq, 0)
		     AND msg.sender_id IS DISTINCT FROM m.user_id
       AND NOT EXISTS (SELECT 1 FROM personal_message_deletions d
         WHERE d.user_id = m.user_id AND d.conv_id = msg.conv_id AND d.message_id = msg.id)
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
func (r *ConversationRepo) UnreadCount(ctx context.Context, convID, userID string) (int32, error) {
	counts, err := r.UnreadCounts(ctx, convID, []string{userID})
	if err != nil {
		return 0, err
	}
	return counts[userID], nil
}

// ListIDsByUser returns the conversations a user belongs to.
func (r *ConversationRepo) ListIDsByUser(ctx context.Context, userID string) ([]string, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("user_id = ?", userID).Pluck("conv_id", &ids).Error
	return ids, err
}

func (r *ConversationRepo) GetReadSeqs(ctx context.Context, convID string) ([]model.ConvReadSeq, error) {
	var seqs []model.ConvReadSeq
	err := r.DB.WithContext(ctx).Model(&model.ConvReadSeq{}).
		Select("conv_read_seqs.*").
		Joins("JOIN conv_members m ON m.conv_id = conv_read_seqs.conv_id AND m.user_id = conv_read_seqs.user_id").
		Where("conv_read_seqs.conv_id = ?", convID).Find(&seqs).Error
	return seqs, err
}

func (r *ConversationRepo) GetReadSeq(ctx context.Context, convID, userID string) (*model.ConvReadSeq, error) {
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

func (r *ConversationRepo) GetReadSeqsByUser(ctx context.Context, convIDs []string, userID string) (map[string]int64, error) {
	if len(convIDs) == 0 {
		return nil, nil
	}
	var rows []struct {
		ConvID      string `gorm:"column:conv_id"`
		LastReadSeq int64  `gorm:"column:last_read_seq"`
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
	m := make(map[string]int64, len(rows))
	for _, row := range rows {
		m[row.ConvID] = row.LastReadSeq
	}
	return m, nil
}

// ========== Settings ==========

func (r *ConversationRepo) GetSettings(ctx context.Context, convID, userID string) (*model.ConvSettings, error) {
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

func (r *ConversationRepo) FindPrivateConv(ctx context.Context, userID1, userID2 string) (*model.Conversation, error) {
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

func (r *ConversationRepo) FindPrivateBotConv(ctx context.Context, userID, botID string) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.DB.WithContext(ctx).Table("conversations c").
		Joins("INNER JOIN conv_members u ON u.conv_id = c.id AND u.member_type = ? AND u.user_id = ?", model.MemberTypeUser, userID).
		Joins("INNER JOIN conv_members b ON b.conv_id = c.id AND b.member_type = ? AND b.bot_id = ?", model.MemberTypeBot, botID).
		Where("c.type = ?", model.ConvTypePrivate).First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// ========== Bot Management ==========

// GetBot reads only the Bot display projection, never the control API (ADR-0007).
func (r *ConversationRepo) GetBot(ctx context.Context, botID string) (*botpb.Bot, error) {
	var bot botpb.Bot
	if err := r.DB.WithContext(ctx).Table("bot.bots").Select("id, name, avatar, owner_type, owner_id, status").Where("id = ?", botID).Take(&bot).Error; err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *ConversationRepo) AddBot(ctx context.Context, bot *model.ConvBot) error {
	return r.DB.WithContext(ctx).Create(bot).Error
}

func (r *ConversationRepo) RemoveBot(ctx context.Context, convID, botID string) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).
		Delete(&model.ConvBot{}).Error
}

func (r *ConversationRepo) UpdateBot(ctx context.Context, convID, botID string, settings any) error {
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

func (r *ConversationRepo) ListBotsByConv(ctx context.Context, convID string) ([]model.ConvBot, error) {
	var bots []model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).Find(&bots).Error
	return bots, err
}

func (r *ConversationRepo) GetBotInConv(ctx context.Context, convID, botID string) (*model.ConvBot, error) {
	var bot model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

// ========== Bot with Member (transactional) ==========

func (r *ConversationRepo) RemoveBotWithMember(ctx context.Context, convID, botID string) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("conv_id = ? AND bot_id = ?", convID, botID).Delete(&model.ConvBot{}).Error; err != nil {
			return err
		}
		return tx.Where("conv_id = ? AND bot_id = ?", convID, botID).Delete(&model.ConversationMember{}).Error
	})
}

func (r *ConversationRepo) GetBotsByIDs(ctx context.Context, ids []string) ([]*botpb.Bot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var bots []*botpb.Bot
	err := r.DB.WithContext(ctx).Table("bot.bots").Select("id, name, avatar").Where("id IN ?", ids).Find(&bots).Error
	return bots, err
}
