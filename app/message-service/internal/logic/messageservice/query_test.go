package messageservicelogic

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
)

func TestBatchGetMessages_Empty(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewBatchGetMessagesLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetMessages(&message.BatchGetMessagesReq{MessageIds: []int64{}})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Messages)
}
