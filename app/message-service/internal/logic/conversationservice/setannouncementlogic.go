package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	messagepb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type SetAnnouncementLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSetAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SetAnnouncementLogic {
	return &SetAnnouncementLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *SetAnnouncementLogic) SetAnnouncement(in *conversation.SetAnnouncementReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}

	// 读取旧公告（用于系统消息 payload）
	oldContent := ""
	if conv, err := l.svcCtx.ConversationRepo.GetConversation(l.ctx, in.ConversationId); err == nil && conv != nil {
		oldContent = conv.Announcement
	}

	if err := l.svcCtx.ConversationRepo.UpdateConversationAnnouncement(l.ctx, in.ConversationId, in.Content); err != nil {
		l.Logger.Errorf("set announcement failed: %v", err)
		return nil, err
	}

	// 发送系统消息
	sendAnnouncementSystemMsg(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "announcement.updated", in.Content, oldContent)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

// sendAnnouncementSystemMsg 发送公告变更系统消息
func sendAnnouncementSystemMsg(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID int64, action, content, oldContent string) {
	if svcCtx.SendSystemMessage == nil {
		return
	}
	detail := content
	if action == "announcement.deleted" {
		detail = "群公告已删除"
	}
	payloadBytes, _ := json.Marshal(map[string]string{
		"content":     content,
		"old_content": oldContent,
	})
	go func() {
		if err := svcCtx.SendSystemMessage(context.Background(), &messagepb.SendSystemMessageReq{
			ConversationId: convID,
			ActorId:        operatorID,
			ActorType:      "user",
			Action:         action,
			Detail:         detail,
			Payload:        string(payloadBytes),
		}); err != nil {
			svcCtx.Logger.WithContext(context.Background()).Errorf("send announcement system message failed: conv=%d action=%s err=%v", convID, action, err)
		}
	}()
}
