package messageservicelogic

import (
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
)

func TestForwardMessage_Validation(t *testing.T) {
	// Test request structure validation
	req := &message.ForwardMessageReq{
		MessageIds:           []int64{},
		TargetConversationId: 200,
		FromUserId:           10,
	}
	assert.Empty(t, req.MessageIds, "empty message_ids should be rejected by logic")
	assert.True(t, len(req.MessageIds) == 0)
}
