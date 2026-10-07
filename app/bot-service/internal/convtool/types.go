package convtool

import (
	"context"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/maomeng/aim/app/bot-service/internal/client"
)

type Input struct {
	LLMClient *client.LlmGatewayClient
	ChatModel einoModel.BaseChatModel
	MsgClient MsgClient
	UserID    string
	ConvID    string
}

type MsgClient interface {
	GetRecentMessages(ctx context.Context, convID, userID string, limit int) ([]Message, error)
}

type Message struct {
	MsgID      string
	SenderID   *string
	SenderName string
	Content    string
	MsgType    int32
	Seq        int64
}
