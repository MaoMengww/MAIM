package messageservicelogic

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"gorm.io/gorm"
)

func modelToPbMessage(msg *model.Message) *message.Message {
	if msg == nil {
		return nil
	}

	pbMsg := &message.Message{
		MessageId:      msg.ID,
		ConversationId: msg.ConvID,
		Seq:            msg.Seq,
		FromUserId:     msg.SenderID,
		Type:           message.MessageType(msg.MsgType),
		Status:         message.MessageStatus(msg.Status),
		ReplyToId:      msg.ReplyToMsgID,
		EditCount:      msg.EditCount,
		CreatedAt:      msg.CreatedAt.Unix(),
		UpdatedAt:      msg.UpdatedAt.Unix(),
	}

	if msg.EditCount > 0 {
		pbMsg.EditedAt = msg.UpdatedAt.Unix()
	}

	content := msg.Content
	if content == nil {
		return pbMsg
	}

	switch msg.MsgType {
	case model.MsgTypeText:
		c := model.ParseTextContent(content)
		pbMsg.Content = &message.Message_Text{Text: &message.TextContent{
			Text:           c.Text,
			MentionUserIds: c.MentionUserIDs,
			MentionAll:     c.MentionAll,
		}}
	case model.MsgTypeImage:
		c := model.ParseImageContent(content)
		pbMsg.Content = &message.Message_Image{Image: &message.ImageContent{
			FileId:       c.FileID,
			Url:          c.URL,
			ThumbnailUrl: c.ThumbnailURL,
			Width:        c.Width,
			Height:       c.Height,
			Size:         c.Size,
			Format:       c.Format,
		}}
	case model.MsgTypeFile:
		c := model.ParseFileContent(content)
		pbMsg.Content = &message.Message_File{File: &message.FileContent{
			FileId:   c.FileID,
			Url:      c.URL,
			Name:     c.Name,
			Size:     c.Size,
			Ext:      c.Ext,
			MimeType: c.MimeType,
		}}
	case model.MsgTypeVideo:
		c := model.ParseVideoContent(content)
		pbMsg.Content = &message.Message_Video{Video: &message.VideoContent{
			FileId:       c.FileID,
			Url:          c.URL,
			ThumbnailUrl: c.ThumbnailURL,
			Duration:     c.Duration,
			Width:        c.Width,
			Height:       c.Height,
			Size:         c.Size,
		}}
	case model.MsgTypeAudio:
		c := model.ParseAudioContent(content)
		pbMsg.Content = &message.Message_Audio{Audio: &message.AudioContent{
			FileId:   c.FileID,
			Url:      c.URL,
			Duration: c.Duration,
			Size:     c.Size,
		}}
	case model.MsgTypeLocation:
		c := model.ParseLocationContent(content)
		pbMsg.Content = &message.Message_Location{Location: &message.LocationContent{
			Latitude:  c.Latitude,
			Longitude: c.Longitude,
			Address:   c.Address,
			Name:      c.Name,
		}}
	case model.MsgTypeSystem:
		c := model.ParseSystemContent(content)
		pbMsg.Content = &message.Message_System{System: &message.SystemContent{
			Action:         c.Action,
			Detail:         c.Detail,
			RelatedUserIds: c.RelatedUserIDs,
		}}
	case model.MsgTypeBot:
		c := model.ParseBotContent(content)
		pbMsg.Content = &message.Message_Bot{Bot: &message.BotContent{
			BotId:          c.BotID,
			BotName:        c.BotName,
			BotAvatar:      c.BotAvatar,
			Text:           c.Text,
			IsStreaming:    c.IsStreaming,
			ThinkingTimeMs: c.ThinkingTimeMs,
			RawPayload:     c.RawPayload,
		}}
	case model.MsgTypeCustom:
		c := model.ParseCustomContent(content)
		pbMsg.Content = &message.Message_Custom{Custom: &message.CustomContent{
			Type: c.Type,
			Data: c.Data,
		}}
	}

	return pbMsg
}

func extractBotContent(req *message.SendBotReplyReq) model.BotContent {
	c := model.BotContent{
		BotID:      req.BotId,
		Text:       req.Text,
		RawPayload: req.RawPayload,
	}
	return c
}

func broadcastContentJSON(content string) model.JSONContent {
	var m model.JSONContent
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		m = model.JSONContent{"raw": content}
	}
	return m
}

func nextSeq(seqRepo *repo.SequenceRepo, db *gorm.DB, ctx context.Context, convID int64) (int64, error) {
	return seqRepo.NextSeq(ctx, db, convID)
}
