package messageservicelogic

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
)

func TestExtractSendContent_Text(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_TEXT,
		Content: &message.SendMessageReq_Text{
			Text: &message.TextContent{
				Text:           "hello world",
				MentionUserIds: []int64{1, 2},
				MentionAll:     false,
			},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, "hello world", result["text"])
	assert.False(t, result["mention_all"].(bool))
}

func TestExtractSendContent_Image(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_IMAGE,
		Content: &message.SendMessageReq_Image{
			Image: &message.ImageContent{
				FileId: 100, Url: "http://img", Width: 800, Height: 600,
			},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, float64(100), result["file_id"])
	assert.Equal(t, "http://img", result["url"])
}

func TestExtractSendContent_File(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_FILE,
		Content: &message.SendMessageReq_File{
			File: &message.FileContent{FileId: 200, Name: "doc.pdf", Size: 1024},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, float64(200), result["file_id"])
	assert.Equal(t, "doc.pdf", result["name"])
}

func TestExtractSendContent_Video(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_VIDEO,
		Content: &message.SendMessageReq_Video{
			Video: &message.VideoContent{FileId: 300, Duration: 120},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, float64(300), result["file_id"])
	assert.Equal(t, float64(120), result["duration"])
}

func TestExtractSendContent_Audio(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_AUDIO,
		Content: &message.SendMessageReq_Audio{
			Audio: &message.AudioContent{FileId: 400, Duration: 60},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, float64(400), result["file_id"])
}

func TestExtractSendContent_Location(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_LOCATION,
		Content: &message.SendMessageReq_Location{
			Location: &message.LocationContent{
				Latitude: 40.7128, Longitude: -74.0060, Address: "NYC",
			},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, 40.7128, result["latitude"])
	assert.Equal(t, "NYC", result["address"])
}

func TestExtractSendContent_Custom(t *testing.T) {
	req := &message.SendMessageReq{
		Type: message.MessageType_MESSAGE_TYPE_CUSTOM,
		Content: &message.SendMessageReq_Custom{
			Custom: &message.CustomContent{Type: "card", Data: `{"title":"hi"}`},
		},
	}
	result := extractSendContent(req)
	assert.Equal(t, "card", result["type"])
}

func TestExtractSendContent_Nil(t *testing.T) {
	req := &message.SendMessageReq{}
	result := extractSendContent(req)
	assert.Empty(t, result)
}

func TestExtractBotContent(t *testing.T) {
	replyTo := int64(5)
	req := &message.SendBotReplyReq{
		BotId:      100,
		Text:       "bot response",
		RawPayload: `{"model":"gpt"}`,
		ReplyToId:  &replyTo,
	}
	c := extractBotContent(req)
	assert.Equal(t, int64(100), c.BotID)
	assert.Equal(t, "bot response", c.Text)
}

func TestExtractBotContent_NoReplyTo(t *testing.T) {
	req := &message.SendBotReplyReq{
		BotId: 200,
		Text:  "hello",
	}
	c := extractBotContent(req)
	assert.Equal(t, int64(200), c.BotID)
	assert.Equal(t, "hello", c.Text)
}

func TestBroadcastContentJSON_Valid(t *testing.T) {
	content := `{"title":"system notice","body":"maintenance at 2am"}`
	c := broadcastContentJSON(content)
	assert.Equal(t, "system notice", c["title"])
	assert.Equal(t, "maintenance at 2am", c["body"])
}

func TestBroadcastContentJSON_Invalid(t *testing.T) {
	content := `not valid json`
	c := broadcastContentJSON(content)
	assert.Equal(t, "not valid json", c["raw"])
}

func TestNextSeq_ContextValid(t *testing.T) {
	// Legacy test: Redis-based nextSeq has been replaced by DB-based SequenceRepo.NextSeq.
	// Kept to satisfy package reference for compile check only.
	_ = context.Background()
	assert.True(t, true)
}
