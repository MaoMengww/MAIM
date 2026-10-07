package messageservicelogic

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math/big"
	"slices"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SendMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendMessageLogic {
	return &SendMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendMessageLogic) SendMessage(in *message.SendMessageReq) (*message.SendMessageResp, error) {
	ctx := l.ctx
	callerID := callerUserID(ctx)
	if callerID == "" {
		return nil, ErrUserIDMissing
	}
	if in == nil || validateIdentities(in.ConversationId, in.FromUserId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid sending identity")
	}
	if callerID != in.FromUserId {
		return nil, ErrSendAsOtherUser
	}
	if identity.ValidateSubmissionKey(in.ClientMsgId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "client_msg_id must be a UUIDv4")
	}
	if in.ReplyToId != nil && identity.Validate(*in.ReplyToId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid reply identity")
	}
	content := extractSendContent(in)
	if err := validateSendContent(in); err != nil {
		return nil, err
	}
	submission, err := originalSubmission(in.Type, content, in.ReplyToId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInvalidParam, "invalid submission content", err)
	}
	senderName := resolveReplySenderName(ctx, l.svcCtx, &in.FromUserId, "user")
	var msg *model.Message
	created := false
	err = l.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		perm, err := l.svcCtx.ConversationRepo.CheckSendPermission(ctx, tx, in.ConversationId, in.FromUserId, model.MemberTypeUser)
		if err != nil {
			return err
		}
		if !perm.IsMember {
			return ErrNotMember
		}
		if perm.IsMutedAll || (perm.IsMuted && (perm.MuteUntil == 0 || time.Now().Unix() < perm.MuteUntil)) {
			return ErrSenderMuted
		}
		if perm.ConversationType == model.ConvTypePrivate && len(perm.OtherMemberIDs) > 0 {
			var blocks []struct{ UserID string }
			if err := tx.Table(`"user".user_blocks`).Select("user_id").
				Clauses(clause.Locking{Strength: "SHARE"}).
				Where("(user_id = ? AND blocked_user_id IN ?) OR (user_id IN ? AND blocked_user_id = ?)",
					in.FromUserId, perm.OtherMemberIDs, perm.OtherMemberIDs, in.FromUserId).
				Limit(1).Find(&blocks).Error; err != nil {
				return ErrBlockCheckFailed
			}
			if len(blocks) > 0 {
				return ErrBlockedByMember
			}
		}
		// Permission locks the conversation, serializing every submission and its
		// side effects. A failed transaction never reserves the client's key.
		var existing model.Message
		err = tx.Where("sender_id = ? AND conv_id = ? AND client_msg_id = ?",
			in.FromUserId, in.ConversationId, in.ClientMsgId).Take(&existing).Error
		if err == nil {
			same, err := sameSubmission(existing.SubmissionContent, submission)
			if err != nil {
				return err
			}
			if !same {
				return ErrContentConflict
			}
			msg = &existing
			return nil
		}
		if !stderrors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := validateReplyTarget(ctx, l.svcCtx, tx, in.ConversationId, in.FromUserId, in.ReplyToId); err != nil {
			return err
		}
		msgID, err := identity.New()
		if err != nil {
			return err
		}
		now := time.Now()
		msg = &model.Message{
			ID: msgID, ConvID: in.ConversationId, SenderID: &in.FromUserId, SenderType: "user",
			ClientMsgID: &in.ClientMsgId, SubmissionContent: submission,
			MsgType: int32(in.Type), Content: content, ReplyToMsgID: in.ReplyToId,
			Status: model.MessageStatusNormal, EditHistory: model.JSONArray{}, CreatedAt: now, UpdatedAt: now,
		}
		if err := persistMessage(ctx, l.svcCtx, tx, msg, senderName); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New(errors.CodeNotFound, "conversation not found")
		}
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "insert message failed", err)
	}
	if created {
		metrics.MessagesSentTotal.Inc(strconv.FormatInt(int64(in.Type), 10))
		l.Infof("message sent: msg_id=%s conv_id=%s sender=%s", msg.ID, in.ConversationId, in.FromUserId)
	}
	return &message.SendMessageResp{MessageId: msg.ID, Seq: msg.Seq, CreatedAt: msg.CreatedAt.Unix()}, nil
}

// Normalize JSON through the persisted representation. Object ordering is not
// semantic, including JSON documents carried in custom-message data.
func originalSubmission(kind message.MessageType, content model.JSONContent, reply *string) (model.JSONContent, error) {
	semantic := make(model.JSONContent, len(content))
	for key, value := range content {
		semantic[key] = value
	}
	var mentions []string
	switch values := semantic["mention_user_ids"].(type) {
	case []string:
		mentions = slices.Clone(values)
	case []any:
		mentions = make([]string, 0, len(values))
		for _, value := range values {
			if id, ok := value.(string); ok {
				mentions = append(mentions, id)
			}
		}
	}
	if _, hasMentions := semantic["mention_user_ids"]; hasMentions {
		slices.Sort(mentions)
		mentions = slices.Compact(mentions)
		if len(mentions) == 0 {
			semantic["mention_user_ids"] = []string{}
		} else {
			semantic["mention_user_ids"] = mentions
		}
	}
	if data, ok := semantic["data"].(string); ok {
		var value any
		decoder := json.NewDecoder(bytes.NewBufferString(data))
		decoder.UseNumber()
		if json.Valid([]byte(data)) && decoder.Decode(&value) == nil {
			semantic["data"] = map[string]any{"json": value}
		}
	}
	return model.JSONContent{"type": int32(kind), "content": semantic, "reply_to_id": reply}, nil
}

func sameSubmission(left, right model.JSONContent) (bool, error) {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	if bytes.Equal(leftJSON, rightJSON) {
		return true, nil
	}
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(leftJSON))
	leftDecoder.UseNumber()
	if err := leftDecoder.Decode(&leftValue); err != nil {
		return false, err
	}
	rightDecoder := json.NewDecoder(bytes.NewReader(rightJSON))
	rightDecoder.UseNumber()
	if err := rightDecoder.Decode(&rightValue); err != nil {
		return false, err
	}
	return equalSubmissionJSON(leftValue, rightValue), nil
}

// JSONB may rewrite numeric lexemes (1e2 -> 100). Compare exact rational
// values, not floating-point approximations, while preserving JSON types.
func equalSubmissionJSON(left, right any) bool {
	switch value := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)
		return ok && value == other
	case string:
		other, ok := right.(string)
		return ok && value == other
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		var leftNumber, rightNumber big.Rat
		if _, ok := leftNumber.SetString(string(value)); !ok {
			return false
		}
		if _, ok := rightNumber.SetString(string(other)); !ok {
			return false
		}
		return leftNumber.Cmp(&rightNumber) == 0
	case []any:
		other, ok := right.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for i := range value {
			if !equalSubmissionJSON(value[i], other[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, entry := range value {
			otherEntry, exists := other[key]
			if !exists || !equalSubmissionJSON(entry, otherEntry) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validateSendContent(in *message.SendMessageReq) error {
	valid := false
	var ids []string
	switch c := in.Content.(type) {
	case *message.SendMessageReq_Text:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_TEXT && c.Text != nil
		ids = c.Text.GetMentionUserIds()
	case *message.SendMessageReq_Image:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_IMAGE && c.Image != nil
		ids = []string{c.Image.GetFileId()}
	case *message.SendMessageReq_File:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_FILE && c.File != nil
		ids = []string{c.File.GetFileId()}
	case *message.SendMessageReq_Video:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_VIDEO && c.Video != nil
		ids = []string{c.Video.GetFileId()}
	case *message.SendMessageReq_Audio:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_AUDIO && c.Audio != nil
		ids = []string{c.Audio.GetFileId()}
	case *message.SendMessageReq_Location:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_LOCATION && c.Location != nil
	case *message.SendMessageReq_Custom:
		valid = in.Type == message.MessageType_MESSAGE_TYPE_CUSTOM && c.Custom != nil
	}
	if !valid || validateIdentities(ids...) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid message content")
	}
	return nil
}

func validateReplyTarget(ctx context.Context, s *svc.ServiceContext, tx *gorm.DB, convID, userID string, reply *string) error {
	if reply == nil {
		return nil
	}
	var target model.Message
	query := tx.Where("id = ? AND conv_id = ?", *reply, convID)
	if userID != "" {
		query = query.Where("NOT EXISTS (SELECT 1 FROM personal_message_deletions d WHERE d.user_id = ? AND d.conv_id = messages.conv_id AND d.message_id = messages.id)", userID)
	}
	if err := query.Take(&target).Error; err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New(errors.CodeInvalidParam, "reply message is unavailable in this conversation")
		}
		return err
	}
	return nil
}

func extractSendContent(req *message.SendMessageReq) model.JSONContent {
	c := req.GetContent()
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *message.SendMessageReq_Text:
		return model.TextContent{
			Text:           v.Text.GetText(),
			MentionUserIDs: v.Text.GetMentionUserIds(),
			MentionAll:     v.Text.GetMentionAll(),
		}.ToJSONContent()
	case *message.SendMessageReq_Image:
		return model.ImageContent{
			FileID:       v.Image.GetFileId(),
			URL:          v.Image.GetUrl(),
			ThumbnailURL: v.Image.GetThumbnailUrl(),
			Width:        v.Image.GetWidth(),
			Height:       v.Image.GetHeight(),
			Size:         v.Image.GetSize(),
			Format:       v.Image.GetFormat(),
		}.ToJSONContent()
	case *message.SendMessageReq_File:
		return model.FileContent{
			FileID:   v.File.GetFileId(),
			URL:      v.File.GetUrl(),
			Name:     v.File.GetName(),
			Size:     v.File.GetSize(),
			Ext:      v.File.GetExt(),
			MimeType: v.File.GetMimeType(),
		}.ToJSONContent()
	case *message.SendMessageReq_Video:
		return model.VideoContent{
			FileID:       v.Video.GetFileId(),
			URL:          v.Video.GetUrl(),
			ThumbnailURL: v.Video.GetThumbnailUrl(),
			Duration:     v.Video.GetDuration(),
			Width:        v.Video.GetWidth(),
			Height:       v.Video.GetHeight(),
			Size:         v.Video.GetSize(),
		}.ToJSONContent()
	case *message.SendMessageReq_Audio:
		return model.AudioContent{
			FileID:   v.Audio.GetFileId(),
			URL:      v.Audio.GetUrl(),
			Duration: v.Audio.GetDuration(),
			Size:     v.Audio.GetSize(),
		}.ToJSONContent()
	case *message.SendMessageReq_Location:
		return model.LocationContent{
			Latitude:  v.Location.GetLatitude(),
			Longitude: v.Location.GetLongitude(),
			Address:   v.Location.GetAddress(),
			Name:      v.Location.GetName(),
		}.ToJSONContent()
	case *message.SendMessageReq_Custom:
		return model.CustomContent{
			Type: v.Custom.GetType(),
			Data: v.Custom.GetData(),
		}.ToJSONContent()
	}
	return nil
}

// buildMessageCreatedPayload builds the Kafka event payload for a new message.
func buildMessageCreatedPayload(msg *model.Message, senderName string) map[string]any {
	return map[string]any{
		"kind":            model.InboxMessageNew,
		"message_id":      msg.ID,
		"conv_id":         msg.ConvID,
		"sender_id":       msg.SenderID,
		"sender_type":     msg.SenderType,
		"msg_type":        int64(msg.MsgType),
		"content":         msg.Content,
		"seq":             msg.Seq,
		"reply_to_msg_id": msg.ReplyToMsgID,
		"created_at":      msg.CreatedAt.Unix(),
		"status":          msg.Status,
		"edit_count":      msg.EditCount,
		"updated_at":      msg.UpdatedAt.Unix(),
		"sender_name":     senderName,
		"preview_text":    model.MessagePreview(msg.MsgType, msg.Content),
	}
}

// persistMessage commits the content, publication intent and conversation tail
// together. The caller holds the conversation lock through transaction commit.
func persistMessage(ctx context.Context, svcCtx *svc.ServiceContext, tx *gorm.DB, msg *model.Message, senderName string) error {
	seq, err := svcCtx.SequenceRepo.NextSeq(ctx, tx, msg.ConvID)
	if err != nil {
		return err
	}
	msg.Seq = seq
	if err := tx.Create(msg).Error; err != nil {
		return err
	}
	if err := svcCtx.PublishInboxChange(ctx, tx, buildMessageCreatedPayload(msg, senderName)); err != nil {
		return err
	}
	return svcCtx.ConversationRepo.TouchLastMessage(ctx, tx, msg.ConvID, msg.ID, seq,
		model.MessagePreview(msg.MsgType, msg.Content))
}
