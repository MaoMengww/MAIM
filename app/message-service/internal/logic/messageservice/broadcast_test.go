package messageservicelogic

import (
	"testing"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	bizerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendBroadcast_Validation(t *testing.T) {
	logic := NewSendBroadcastLogic(t.Context(), &svc.ServiceContext{})
	resp, err := logic.SendBroadcast(&message.SendBroadcastReq{Content: "", Scope: "all"})
	require.ErrorIs(t, err, ErrBroadcastContentRequired)
	assert.Nil(t, resp)
}

func TestSendBroadcastRejectsUnknownScope(t *testing.T) {
	logic := NewSendBroadcastLogic(t.Context(), &svc.ServiceContext{})
	_, err := logic.SendBroadcast(&message.SendBroadcastReq{SenderId: 10, Content: `{"text":"notice"}`, Scope: "unknown"})
	biz, ok := bizerrors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, bizerrors.CodeInvalidParam, biz.Code)
}
