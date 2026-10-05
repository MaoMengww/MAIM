package messageservicelogic

import (
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
