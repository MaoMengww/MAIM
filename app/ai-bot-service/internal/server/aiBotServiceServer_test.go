package server

import (
	"testing"

	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	"github.com/stretchr/testify/require"
)

func TestBuildRuntimeEventFromStreamChatReq(t *testing.T) {
	req := &aibot.StreamChatReq{
		BotId:        100,
		ConvId:       200,
		Message:      "policy question",
		ReplyToMsgId: 300,
	}
	event := buildRuntimeEvent(req, 9, "alice", "zh-CN")
	require.Equal(t, int64(200), event.ConvID)
	require.Equal(t, int64(9), event.Sender.UserID)
	require.Equal(t, "alice", event.Sender.Username)
	require.Equal(t, "policy question", event.Message.Text)
	require.Equal(t, int64(300), event.Message.ReplyToMsgID)
}
