package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
)

type MessageRepo struct {
	db     *database.DB
	userID int64
}

func NewMessageRepo(db *database.DB) *MessageRepo {
	return &MessageRepo{db: db}
}

// ForUser scopes every client read before pagination or hydration. The original
// repository remains unscoped for internal mutations and service reads.
func (r *MessageRepo) ForUser(userID int64) *MessageRepo {
	return &MessageRepo{db: r.db, userID: userID}
}

func (r *MessageRepo) readQuery(ctx context.Context) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Message{})
	if r.userID != 0 {
		q = q.Where(`NOT EXISTS (SELECT 1 FROM personal_message_deletions d
   WHERE d.user_id = ? AND d.conv_id = messages.conv_id AND d.message_id = messages.id)`, r.userID).
			Where(`EXISTS (SELECT 1 FROM conv_members m WHERE m.conv_id = messages.conv_id AND m.user_id = ?)`, r.userID)
	}
	return q
}

func (r *MessageRepo) InsertPersonalDeletion(ctx context.Context, tx *gorm.DB, userID, convID, messageID int64) (bool, error) {
	result := tx.WithContext(ctx).Exec(`INSERT INTO personal_message_deletions (user_id, conv_id, message_id)
  VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, userID, convID, messageID)
	return result.RowsAffected > 0, result.Error
}

// PersonalDeletedIDs lets the search index apply account visibility before its
// total, aggregations and page boundary; it never scans message history.
func (r *MessageRepo) PersonalDeletedIDs(ctx context.Context, userID int64, convIDs []int64) ([]int64, error) {
	var ids []int64
	if len(convIDs) == 0 {
		return ids, nil
	}
	err := r.db.WithContext(ctx).Model(&model.PersonalMessageDeletion{}).
		Where("user_id = ? AND conv_id IN ?", userID, convIDs).Pluck("message_id", &ids).Error
	return ids, err
}

// ProjectConversationPreviews replaces only hidden tails. Each lateral lookup
// uses the conversation/sequence index, and the entire page is one query.
func (r *MessageRepo) ProjectConversationPreviews(ctx context.Context, userID int64, convs []model.Conversation) error {
	if userID == 0 || len(convs) == 0 {
		return nil
	}
	ids := make([]int64, len(convs))
	lastIDs := make([]int64, len(convs))
	for i := range convs {
		ids[i] = convs[i].ID
		lastIDs[i] = convs[i].LastMessageID
	}
	var latest []model.Message
	// Filter the tails actually returned to the caller, not a newer database tail.
	// Message IDs are globally unique, so matching both sets preserves each pair.
	err := r.db.WithContext(ctx).Raw(`SELECT hidden.conv_id, COALESCE(last.id, 0) AS id, last.msg_type, last.content, last.status
  FROM personal_message_deletions hidden
  LEFT JOIN LATERAL (SELECT msg.id, msg.msg_type, msg.content, msg.status FROM messages msg
    WHERE msg.conv_id = hidden.conv_id AND NOT EXISTS (SELECT 1 FROM personal_message_deletions d
     WHERE d.user_id = ? AND d.conv_id = msg.conv_id AND d.message_id = msg.id)
    ORDER BY msg.seq DESC LIMIT 1) last ON TRUE
  WHERE hidden.user_id = ? AND hidden.conv_id IN ? AND hidden.message_id IN ?`, userID, userID, ids, lastIDs).Scan(&latest).Error
	if err != nil {
		return err
	}
	byConv := make(map[int64]model.Message, len(latest))
	for _, msg := range latest {
		byConv[msg.ConvID] = msg
	}
	for i := range convs {
		if msg, ok := byConv[convs[i].ID]; ok {
			convs[i].LastMessageID = msg.ID
			convs[i].LastMessagePreview = ""
			if msg.ID != 0 {
				convs[i].LastMessagePreview = model.MessagePreview(msg.MsgType, msg.Content)
				if msg.Status == model.MessageStatusRecalled {
					convs[i].LastMessagePreview = "[消息已撤回]"
				}
			}
		}
	}
	return nil
}

func (r *MessageRepo) GetByID(ctx context.Context, id int64) (*model.Message, error) {
	var msg model.Message
	err := r.readQuery(ctx).Where("id = ?", id).First(&msg).Error
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (r *MessageRepo) GetByIDs(ctx context.Context, ids []int64) ([]model.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var msgs []model.Message
	err := r.readQuery(ctx).Where("id IN ?", ids).Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) GetByConvID(ctx context.Context, convID int64, limit int, cursor int64, beforeTime, afterTime int64, filterTypes []int32) ([]model.Message, error) {
	q := r.readQuery(ctx).Where("conv_id = ?", convID)

	if cursor > 0 {
		q = q.Where("seq < ?", cursor)
	}
	if beforeTime > 0 {
		q = q.Where("created_at <= to_timestamp(?)", beforeTime)
	}
	if afterTime > 0 {
		q = q.Where("created_at >= to_timestamp(?)", afterTime)
	}
	if len(filterTypes) > 0 {
		q = q.Where("msg_type IN ?", filterTypes)
	}

	var msgs []model.Message
	err := q.Order("seq DESC").Limit(limit).Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) GetAroundSeq(ctx context.Context, convID int64, seq int64, limit int32) ([]model.Message, error) {
	half := int64(limit / 2)
	var msgs []model.Message
	err := r.readQuery(ctx).
		Where("conv_id = ? AND seq BETWEEN ? AND ?", convID, seq-half, seq+half).
		Order("seq ASC").
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) UpdateContentWithTx(ctx context.Context, tx *gorm.DB, id int64, content model.JSONContent, editHistory model.JSONArray, editCount int32) error {
	return tx.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"content":      content,
			"edit_history": editHistory,
			"edit_count":   editCount,
			"status":       model.MessageStatusEdited,
		}).Error
}

func (r *MessageRepo) UpdateStatusWithTx(ctx context.Context, tx *gorm.DB, id int64, status int32) error {
	return tx.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", id).
		Update("status", status).Error
}

func (r *MessageRepo) DeleteWithTx(ctx context.Context, tx *gorm.DB, id int64) error {
	return tx.WithContext(ctx).Delete(&model.Message{}, id).Error
}
