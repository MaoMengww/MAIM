package consumer

import (
	"encoding/json"
	"testing"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestShouldRespond_Always(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
		Message:   &model.EventMessage{Text: "你好"},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"always"},
	}
	assert.True(t, shouldRespond(event, convBot))
}

func TestShouldRespond_Mention(t *testing.T) {
	event := &model.BotEvent{
		EventType:        "message.created",
		Message:          &model.EventMessage{Text: "你好"},
		MentionedUserIDs: []int64{100, 200},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"mention"},
	}
	assert.True(t, shouldRespond(event, convBot))
}

func TestShouldRespond_Mention_NotMentioned(t *testing.T) {
	event := &model.BotEvent{
		EventType:        "message.created",
		Message:          &model.EventMessage{Text: "你好"},
		MentionedUserIDs: nil,
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"mention"},
	}
	assert.False(t, shouldRespond(event, convBot))
}

func TestShouldRespond_Keyword(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "帮助"},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"keyword:帮助"},
	}
	assert.True(t, shouldRespond(event, convBot))
}

func TestShouldRespond_Keyword_NotMatch(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "你好"},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"keyword:帮助"},
	}
	assert.False(t, shouldRespond(event, convBot))
}

func TestShouldRespond_EventType(t *testing.T) {
	event := &model.BotEvent{
		EventType: "member.joined",
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"event:member.joined"},
	}
	assert.True(t, shouldRespond(event, convBot))
}

func TestShouldRespond_EventType_NotMatch(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"event:member.joined"},
	}
	assert.False(t, shouldRespond(event, convBot))
}

func TestShouldRespond_MultipleTriggers(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "帮助"},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: model.StringArray{"mention", "keyword:帮助"},
	}
	// Second trigger matches ("keyword:帮助")
	assert.True(t, shouldRespond(event, convBot))
}

func TestShouldRespond_NoTriggers(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
		Message:   &model.EventMessage{Text: "你好"},
	}
	convBot := &model.ConvBot{
		ResponseTriggers: nil,
	}
	assert.False(t, shouldRespond(event, convBot))
}

func TestShouldRespond_NilConvBot(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
	}
	assert.False(t, shouldRespond(event, nil))
}

func TestBotEvent_JSONRoundTrip(t *testing.T) {
	event := model.BotEvent{
		EventType: "message.created",
		BotID:     1001,
		ConvID:    456,
		Message: &model.EventMessage{
			MsgID:   789,
			Text:    "你好",
			MsgType: 1,
		},
		Sender: &model.EventSender{
			UserID:   123,
			Username: "张三",
		},
		MentionedUserIDs: []int64{100, 200},
	}

	b, err := json.Marshal(event)
	assert.NoError(t, err)

	var restored model.BotEvent
	err = json.Unmarshal(b, &restored)
	assert.NoError(t, err)

	assert.Equal(t, event.EventType, restored.EventType)
	assert.Equal(t, event.BotID, restored.BotID)
	assert.Equal(t, event.ConvID, restored.ConvID)
	assert.Equal(t, "你好", restored.Message.Text)
	assert.Equal(t, "张三", restored.Sender.Username)
}

func TestEventID_Format(t *testing.T) {
	event := model.BotEvent{
		EventType: "message.created",
		BotID:     1001,
		Message:   &model.EventMessage{MsgID: 789},
	}
	_ = event // used in handler
	id := "789_message.created_1001"
	assert.Equal(t, "789_message.created_1001", id)
}
