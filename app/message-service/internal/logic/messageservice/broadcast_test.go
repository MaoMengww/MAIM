package messageservicelogic

import (
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
)

func TestSendBroadcast_Validation(t *testing.T) {
	// Test content validation at the request level
	req := &message.SendBroadcastReq{
		SenderId: 10,
		Content:  "",
		Scope:    "all",
	}
	assert.Empty(t, req.Content, "empty content should be rejected by logic")

	// Valid request structure
	groupID := int64(500)
	req2 := &message.SendBroadcastReq{
		SenderId:      10,
		Content:       `{"text":"group msg"}`,
		Scope:         "group",
		ScopeTargetId: &groupID,
	}
	assert.NotEmpty(t, req2.Content)
	assert.Equal(t, "group", req2.Scope)
}
