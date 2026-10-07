package consumer

import (
	"testing"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestShouldRespond_Always(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
		Message:   &model.EventMessage{Text: "你好"},
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"always"},
	}
	assert.True(t, shouldRespond(event, bot))
}

func TestShouldRespond_Mention(t *testing.T) {
	event := &model.BotEvent{
		EventType:        "message.created",
		Message:          &model.EventMessage{Text: "你好"},
		MentionedUserIDs: []string{"01960000-0000-7000-8000-000000000001", "01960000-0000-7000-8000-000000000002"},
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"mention"},
	}
	assert.True(t, shouldRespond(event, bot))
}

func TestShouldRespond_Mention_NotMentioned(t *testing.T) {
	event := &model.BotEvent{
		EventType:        "message.created",
		Message:          &model.EventMessage{Text: "你好"},
		MentionedUserIDs: nil,
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"mention"},
	}
	assert.False(t, shouldRespond(event, bot))
}

func TestShouldRespond_Keyword(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "帮助"},
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"keyword:帮助"},
	}
	assert.True(t, shouldRespond(event, bot))
}

func TestShouldRespond_Keyword_NotMatch(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "你好"},
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"keyword:帮助"},
	}
	assert.False(t, shouldRespond(event, bot))
}

func TestShouldRespond_EventType(t *testing.T) {
	event := &model.BotEvent{
		EventType: "member.joined",
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"event:member.joined"},
	}
	assert.True(t, shouldRespond(event, bot))
}

func TestShouldRespond_EventType_NotMatch(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"event:member.joined"},
	}
	assert.False(t, shouldRespond(event, bot))
}

func TestShouldRespond_MultipleTriggers(t *testing.T) {
	event := &model.BotEvent{
		Message: &model.EventMessage{Text: "帮助"},
	}
	bot := &model.Bot{
		ResponseTriggers: []string{"mention", "keyword:帮助"},
	}
	// Second trigger matches ("keyword:帮助")
	assert.True(t, shouldRespond(event, bot))
}

func TestShouldRespond_NoTriggers(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
		Message:   &model.EventMessage{Text: "你好"},
	}
	bot := &model.Bot{
		ResponseTriggers: nil,
	}
	assert.False(t, shouldRespond(event, bot))
}

func TestShouldRespond_NilBot(t *testing.T) {
	event := &model.BotEvent{
		EventType: "message.created",
	}
	assert.False(t, shouldRespond(event, nil))
}
