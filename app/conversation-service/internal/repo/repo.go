package repo

import (
	"context"
	"time"

	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RepoInterface interface {
	CreateConversation(ctx context.Context, conv *model.Conversation) error
	GetConversation(ctx context.Context, id int64) (*model.Conversation, error)
	ListConversationsByUser(ctx context.Context, userID int64, cursor int64, limit int, typ *int32) ([]model.Conversation, error)
	ListConversationsByUserPinned(ctx context.Context, userID int64, pinned bool) ([]model.Conversation, error)
	UpdateConversation(ctx context.Context, conv *model.Conversation) error
	UpdateConversationAnnouncement(ctx context.Context, id int64, announcement string) error
	UpdateConversationMutedAll(ctx context.Context, id int64, mutedAll bool) error
	UpdateConversationOwner(ctx context.Context, id int64, newOwnerID int64) error
	DeleteConversation(ctx context.Context, id int64) error
	UpdateConversationLastMessage(ctx context.Context, id int64, msgID int64, preview string, seq int64) error
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
	UpsertReadSeq(ctx context.Context, convID, userID int64, seq int64, id int64) error
	GetReadSeqs(ctx context.Context, convID int64) ([]model.ConvReadSeq, error)
	GetReadSeq(ctx context.Context, convID, userID int64) (*model.ConvReadSeq, error)
	GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error)
	GetSettings(ctx context.Context, convID, userID int64) (*model.ConvSettings, error)
	UpsertSettings(ctx context.Context, s *model.ConvSettings) error

	FindPrivateConv(ctx context.Context, userID1, userID2 int64) (*model.Conversation, error)

	GetBot(ctx context.Context, botID int64) (*model.Bot, error)
	AddBot(ctx context.Context, bot *model.ConvBot) error
	RemoveBot(ctx context.Context, convID, botID int64) error
	UpdateBot(ctx context.Context, convID, botID int64, triggers []string, settings any) error
	ListBotsByConv(ctx context.Context, convID int64) ([]model.ConvBot, error)
	GetBotInConv(ctx context.Context, convID, botID int64) (*model.ConvBot, error)
	AddBotWithMember(ctx context.Context, bot *model.ConvBot, member *model.ConversationMember) error
	RemoveBotWithMember(ctx context.Context, convID, botID int64) error
	GetBotsByIDs(ctx context.Context, ids []int64) ([]model.Bot, error)
}

type Repo struct {
	DB             *database.DB
	BotPlatformRpc botplatform.BotPlatformClient
}

func NewRepo(db *database.DB, botRpc botplatform.BotPlatformClient) *Repo {
	return &Repo{DB: db, BotPlatformRpc: botRpc}
}

// ========== Conversation CRUD ==========

func (r *Repo) CreateConversation(ctx context.Context, conv *model.Conversation) error {
	return r.DB.WithContext(ctx).Create(conv).Error
}

func (r *Repo) GetConversation(ctx context.Context, id int64) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func (r *Repo) ListConversationsByUser(ctx context.Context, userID int64, cursor int64, limit int, typ *int32) ([]model.Conversation, error) {
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

func (r *Repo) ListConversationsByUserPinned(ctx context.Context, userID int64, pinned bool) ([]model.Conversation, error) {
	var convs []model.Conversation
	err := r.DB.WithContext(ctx).
		Table("conversations c").
		Joins("INNER JOIN conv_settings cs ON cs.conv_id = c.id AND cs.user_id = ? AND cs.is_pinned = ?", userID, pinned).
		Order("c.updated_at DESC").Find(&convs).Error
	return convs, err
}

func (r *Repo) UpdateConversation(ctx context.Context, conv *model.Conversation) error {
	return r.DB.WithContext(ctx).Model(conv).Where("id = ?", conv.ID).
		Updates(map[string]interface{}{
			"name":       conv.Name,
			"avatar":     conv.Avatar,
			"background": conv.Background,
			"updated_at": time.Now(),
		}).Error
}

func (r *Repo) UpdateConversationAnnouncement(ctx context.Context, id int64, announcement string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"announcement": announcement, "updated_at": time.Now()}).Error
}

func (r *Repo) UpdateConversationMutedAll(ctx context.Context, id int64, mutedAll bool) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"is_muted_all": mutedAll, "updated_at": time.Now()}).Error
}

func (r *Repo) UpdateConversationOwner(ctx context.Context, id int64, newOwnerID int64) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{"owner_id": newOwnerID, "updated_at": time.Now()}).Error
}

func (r *Repo) DeleteConversation(ctx context.Context, id int64) error {
	return r.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.Conversation{}).Error
}

func (r *Repo) UpdateConversationLastMessage(ctx context.Context, id int64, msgID int64, preview string, seq int64) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"last_message_id":      msgID,
			"last_message_preview": preview,
			"max_seq":              seq,
			"updated_at":           time.Now(),
		}).Error
}

func (r *Repo) IncrementMemberCount(ctx context.Context, id int64, delta int) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).
		Update("member_count", gorm.Expr("member_count + ?", delta)).Error
}

// ========== Members ==========

func (r *Repo) AddMember(ctx context.Context, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Create(member).Error
}

func (r *Repo) AddMembersBatch(ctx context.Context, members []model.ConversationMember) error {
	if len(members) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Create(&members).Error
}

func (r *Repo) RemoveMember(ctx context.Context, convID, userID int64) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).
		Delete(&model.ConversationMember{}).Error
}

func (r *Repo) GetMember(ctx context.Context, convID, userID int64) (*model.ConversationMember, error) {
	var m model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *Repo) GetMembers(ctx context.Context, convID int64, offset, limit int) ([]model.ConversationMember, error) {
	var members []model.ConversationMember
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).
		Order("role ASC, joined_at ASC").Offset(offset).Limit(limit).Find(&members).Error
	return members, err
}

func (r *Repo) CountMembers(ctx context.Context, convID int64) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).Where("conv_id = ?", convID).Count(&count).Error
	return count, err
}

func (r *Repo) UpdateMember(ctx context.Context, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Model(member).Where("conv_id = ? AND user_id = ?", member.ConvID, member.UserID).
		Updates(map[string]interface{}{"role": member.Role, "alias": member.Alias}).Error
}

func (r *Repo) UpdateMemberRole(ctx context.Context, convID, userID int64, role int32) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Update("role", role).Error
}

func (r *Repo) IsMember(ctx context.Context, convID, userID int64) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).Count(&count).Error
	return count > 0, err
}

// ========== Mute ==========

func (r *Repo) MuteMember(ctx context.Context, convID, userID int64, muteUntil int64) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": true, "mute_until": muteUntil}).Error
}

func (r *Repo) UnmuteMember(ctx context.Context, convID, userID int64) error {
	return r.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND user_id = ?", convID, userID).
		Updates(map[string]interface{}{"is_muted": false, "mute_until": 0}).Error
}

// ========== Read Status ==========

func (r *Repo) UpsertReadSeq(ctx context.Context, convID, userID int64, seq int64, id int64) error {
	return r.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "conv_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_read_seq", "read_at"}),
		}).
		Create(&model.ConvReadSeq{
			ID:          id,
			ConvID:      convID,
			UserID:      userID,
			LastReadSeq: seq,
			ReadAt:      time.Now(),
		}).Error
}

func (r *Repo) GetReadSeqs(ctx context.Context, convID int64) ([]model.ConvReadSeq, error) {
	var seqs []model.ConvReadSeq
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).Find(&seqs).Error
	return seqs, err
}

func (r *Repo) GetReadSeq(ctx context.Context, convID, userID int64) (*model.ConvReadSeq, error) {
	var rs model.ConvReadSeq
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).First(&rs).Error
	if err != nil {
		return nil, err
	}
	return &rs, nil
}

func (r *Repo) GetReadSeqsByUser(ctx context.Context, convIDs []int64, userID int64) (map[int64]int64, error) {
	if len(convIDs) == 0 {
		return nil, nil
	}
	var rows []struct {
		ConvID      int64 `gorm:"column:conv_id"`
		LastReadSeq int64 `gorm:"column:last_read_seq"`
	}
	err := r.DB.WithContext(ctx).
		Model(&model.ConvReadSeq{}).
		Select("conv_id, last_read_seq").
		Where("conv_id IN ? AND user_id = ?", convIDs, userID).
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

func (r *Repo) GetSettings(ctx context.Context, convID, userID int64) (*model.ConvSettings, error) {
	var s model.ConvSettings
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND user_id = ?", convID, userID).First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) UpsertSettings(ctx context.Context, s *model.ConvSettings) error {
	return r.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "conv_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"is_muted", "is_pinned"}),
		}).
		Create(s).Error
}

// ========== Private Conversation ==========

func (r *Repo) FindPrivateConv(ctx context.Context, userID1, userID2 int64) (*model.Conversation, error) {
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

func (r *Repo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	if r.BotPlatformRpc != nil {
		pbBot, err := r.BotPlatformRpc.GetBot(ctx, &botplatform.GetBotReq{BotId: botID})
		if err != nil {
			return nil, err
		}
		return &model.Bot{
			ID:     pbBot.Id,
			Name:   pbBot.Name,
			Avatar: pbBot.Avatar,
		}, nil
	}
	// fallback: direct DB query
	var bot model.Bot
	err := r.DB.WithContext(ctx).Where("id = ?", botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *Repo) AddBot(ctx context.Context, bot *model.ConvBot) error {
	return r.DB.WithContext(ctx).Create(bot).Error
}

func (r *Repo) RemoveBot(ctx context.Context, convID, botID int64) error {
	return r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).
		Delete(&model.ConvBot{}).Error
}

func (r *Repo) UpdateBot(ctx context.Context, convID, botID int64, triggers []string, settings any) error {
	updates := map[string]any{}
	if triggers != nil {
		updates["response_triggers"] = triggers
	}
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

func (r *Repo) ListBotsByConv(ctx context.Context, convID int64) ([]model.ConvBot, error) {
	var bots []model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ?", convID).Find(&bots).Error
	return bots, err
}

func (r *Repo) GetBotInConv(ctx context.Context, convID, botID int64) (*model.ConvBot, error) {
	var bot model.ConvBot
	err := r.DB.WithContext(ctx).Where("conv_id = ? AND bot_id = ?", convID, botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

// ========== Bot with Member (transactional) ==========

func (r *Repo) AddBotWithMember(ctx context.Context, bot *model.ConvBot, member *model.ConversationMember) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(bot).Error; err != nil {
			return err
		}
		return tx.Create(member).Error
	})
}

func (r *Repo) RemoveBotWithMember(ctx context.Context, convID, botID int64) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("conv_id = ? AND bot_id = ?", convID, botID).Delete(&model.ConvBot{}).Error; err != nil {
			return err
		}
		return tx.Where("conv_id = ? AND user_id = ?", convID, botID).Delete(&model.ConversationMember{}).Error
	})
}

func (r *Repo) GetBotsByIDs(ctx context.Context, ids []int64) ([]model.Bot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if r.BotPlatformRpc != nil {
		resp, err := r.BotPlatformRpc.BatchGetBots(ctx, &botplatform.BatchGetBotsReq{BotIds: ids})
		if err != nil {
			return nil, err
		}
		bots := make([]model.Bot, len(resp.Bots))
		for i, pb := range resp.Bots {
			bots[i] = model.Bot{ID: pb.Id, Name: pb.Name, Avatar: pb.Avatar}
		}
		return bots, nil
	}
	// fallback: direct DB query
	var bots []model.Bot
	err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&bots).Error
	return bots, err
}

// ========== Transaction ==========

func (r *Repo) BeginTx(ctx context.Context) (*gorm.DB, error) {
	return r.DB.WithContext(ctx).Begin(), nil
}
