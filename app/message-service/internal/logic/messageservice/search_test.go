package messageservicelogic

import (
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
)

func TestSearchMessages_NoCondition_Err(t *testing.T) {
	tctx := newTestSvcCtx(t)
	logic := NewSearchMessagesLogic(t.Context(), tctx.SvcCtx)
	convID := int64(100)

	_, err := logic.SearchMessages(&message.SearchMessagesReq{
		UserId:         1,
		ConversationId: &convID,
		Pagination:     &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "condition")
}

func TestSearchMessages_NoConversationID_Err(t *testing.T) {
	tctx := newTestSvcCtx(t)
	logic := NewSearchMessagesLogic(t.Context(), tctx.SvcCtx)

	_, err := logic.SearchMessages(&message.SearchMessagesReq{
		UserId:     1,
		Keyword:    "hello",
		Pagination: &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "es client")
}

func TestSearchMessages_InvalidSenderType_Err(t *testing.T) {
	tctx := newTestSvcCtx(t)
	logic := NewSearchMessagesLogic(t.Context(), tctx.SvcCtx)
	convID := int64(100)

	_, err := logic.SearchMessages(&message.SearchMessagesReq{
		UserId:         1,
		ConversationId: &convID,
		Keyword:        "hello",
		SenderType:     "invalid",
		Pagination:     &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sender_type")
}

func TestSearchMessages_InvalidTimeRange_Err(t *testing.T) {
	tctx := newTestSvcCtx(t)
	logic := NewSearchMessagesLogic(t.Context(), tctx.SvcCtx)
	convID := int64(100)
	start := int64(2000)
	end := int64(1000)

	_, err := logic.SearchMessages(&message.SearchMessagesReq{
		UserId:         1,
		ConversationId: &convID,
		Keyword:        "hello",
		StartTime:      &start,
		EndTime:        &end,
		Pagination:     &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "time range")
}
