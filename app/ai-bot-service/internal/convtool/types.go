package convtool

import (
	"context"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
)

type Input struct {
	LLMClient *client.LlmGatewayClient
	ChatModel einoModel.BaseChatModel
	MsgClient MsgClient
	UserID    int64
	ConvID    int64
}

type MsgClient interface {
	GetRecentMessages(ctx context.Context, convID, userID int64, limit int) ([]Message, error)
}

type Message struct {
	MsgID    int64
	SenderID int64
	Content  string
	MsgType  int32
	Seq      int64
}
