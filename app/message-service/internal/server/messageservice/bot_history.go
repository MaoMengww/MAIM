package server

import (
	"context"
	messageservicelogic "github.com/maomeng/aim/app/message-service/internal/logic/messageservice"
	"github.com/maomeng/aim/app/message-service/pb/message"
)

func (s *MessageServiceServer) GrantBotHistory(ctx context.Context, in *message.GrantBotHistoryReq) (*message.GrantBotHistoryResp, error) {
	return messageservicelogic.GrantBotHistory(ctx, s.svcCtx, in)
}
