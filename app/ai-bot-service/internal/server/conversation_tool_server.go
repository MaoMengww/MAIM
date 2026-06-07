package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
	"github.com/maomeng/aim/app/ai-bot-service/internal/convtool"
	"github.com/maomeng/aim/app/ai-bot-service/internal/repo"
	"github.com/maomeng/aim/app/ai-bot-service/internal/svc"
	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/interceptor"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ConversationToolServer struct {
	svcCtx *svc.ServiceContext
	aibot.UnimplementedConversationToolServiceServer
}

func NewConversationToolServer(svcCtx *svc.ServiceContext) *ConversationToolServer {
	return &ConversationToolServer{svcCtx: svcCtx}
}

func (s *ConversationToolServer) getUserID(ctx context.Context) int64 {
	if v, ok := ctx.Value(interceptor.ContextKeyUserID).(int64); ok {
		return v
	}
	return 0
}

func (s *ConversationToolServer) getLLMInput(ctx context.Context, userID, convID int64) (*convtool.Input, error) {
	userCtx := metadata.AppendToOutgoingContext(ctx, "user-id", strconv.FormatInt(userID, 10))
	userClient := userpb.NewUserServiceClient(s.svcCtx.UserServiceConn.Conn())
	settingsResp, err := userClient.GetSettings(userCtx, &commonpb.Empty{})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取用户设置失败", err)
	}
	if settingsResp.AiModelId == 0 {
		return nil, errors.New(errors.CodeInvalidParam, "请先在设置-个人资料中配置AI助手模型")
	}

	llmClient := client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn)
	chatModel := llmClient.NewEinoChatModel(settingsResp.AiModelId, settingsResp.AiModelName, userID)

	return &convtool.Input{
		LLMClient: llmClient,
		ChatModel: chatModel,
		UserID:    userID,
		ConvID:    convID,
	}, nil
}

// pushAsyncResult pushes an async operation result to a user via ws-gateway.
func (s *ConversationToolServer) pushAsyncResult(userID int64, eventType string, data map[string]any) {
	wsClient := client.NewWsGatewayClient(s.svcCtx.WsGatewayConn)
	msg := map[string]any{
		"type": eventType,
	}
	for k, v := range data {
		msg[k] = v
	}
	b, err := json.Marshal(msg)
	if err != nil {
		s.svcCtx.Logger.Errorf("pushAsyncResult marshal failed: type=%s err=%v", eventType, err)
		return
	}
	if err := wsClient.PushToUser(context.Background(), userID, b); err != nil {
		s.svcCtx.Logger.Errorf("pushAsyncResult push failed: type=%s user=%d err=%v", eventType, userID, err)
	}
}

// SummarizeConversation summarizes a conversation asynchronously.
// It validates the request and returns immediately with status "processing".
// The actual LLM call and persistence run in a background goroutine.
// When done, the result is pushed to the user via WebSocket (type: conv.summarize.done).
func (s *ConversationToolServer) SummarizeConversation(ctx context.Context, req *aibot.SummarizeReq) (*aibot.SummarizeResp, error) {
	// Fast validation
	input, err := s.getLLMInput(ctx, req.UserId, req.ConvId)
	if err != nil {
		return nil, err
	}

	msgClient := client.NewMessageClient(s.svcCtx.MessageSvcConn)

	maxCount := 100
	switch r := req.Range.(type) {
	case *aibot.SummarizeReq_LastMessageCount:
		maxCount = int(r.LastMessageCount)
	case *aibot.SummarizeReq_All:
		maxCount = 1000
	}

	allMsgs, err := msgClient.GetAllMessages(ctx, req.ConvId, req.UserId, maxCount)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取消息失败", err)
	}
	if len(allMsgs) == 0 {
		return nil, errors.New(errors.CodeInvalidParam, "没有可总结的消息")
	}

	// Build messages in chronological order (reversed from GetAllMessages)
	msgs := make([]convtool.Message, len(allMsgs))
	for i, m := range allMsgs {
		msgs[len(allMsgs)-1-i] = convtool.Message{
			MsgID:    m.MsgID,
			SenderID: m.SenderID,
			Content:  m.Content,
			MsgType:  int32(m.MsgType),
			Seq:      m.Seq,
		}
	}

	messageText := convtool.BuildMessageText(msgs)
	totalCount := len(msgs)

	// Spawn async processing
	go func() {
		bgCtx := context.Background()
		result, llmErr := convtool.Summarize(bgCtx, input, messageText, totalCount)
		if llmErr != nil {
			s.svcCtx.Logger.Errorf("async summarize failed: conv=%d user=%d err=%v", req.ConvId, req.UserId, llmErr)
			s.pushAsyncResult(req.UserId, "conv.summarize.failed", map[string]any{
				"conv_id": strconv.FormatInt(req.ConvId, 10),
				"error":   llmErr.Error(),
			})
			return
		}

		// Persist summary
		summaryRepo := repo.NewConvSummaryRepo(s.svcCtx.DB)
		summary := &repo.ConvSummary{
			ConvID:       req.ConvId,
			UserID:       req.UserId,
			RangeType:    fmt.Sprintf("count_%d", maxCount),
			MessageCount: result.TotalMessages,
			Summary:      result.Summary,
		}
		if err := summaryRepo.Create(bgCtx, summary); err != nil {
			s.svcCtx.Logger.Errorf("async save summary failed: %v", err)
		}

		// Persist todos
		todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
		var pbTodos []map[string]any
		for _, todoText := range result.Todos {
			t := &repo.SummaryTodo{
				SummaryID: summary.ID,
				ConvID:    req.ConvId,
				Content:   todoText,
			}
			if err := todoRepo.Create(bgCtx, t); err != nil {
				s.svcCtx.Logger.Errorf("async save todo failed: %v", err)
				continue
			}
			pbTodos = append(pbTodos, map[string]any{
				"id":         strconv.FormatInt(t.ID, 10),
				"summary_id": strconv.FormatInt(t.SummaryID, 10),
				"conv_id":    strconv.FormatInt(t.ConvID, 10),
				"content":    t.Content,
				"done":       t.Done,
				"created_at": t.CreatedAt.Unix(),
			})
		}

		// Push result via WebSocket
		s.pushAsyncResult(req.UserId, "conv.summarize.done", map[string]any{
			"conv_id":        strconv.FormatInt(req.ConvId, 10),
			"summary_id":     strconv.FormatInt(summary.ID, 10),
			"summary":        result.Summary,
			"todos":          pbTodos,
			"total_messages": result.TotalMessages,
			"created_at":     summary.CreatedAt.Unix(),
		})

		s.svcCtx.Logger.Infof("async summarize done: conv=%d user=%d summary_id=%d todos=%d",
			req.ConvId, req.UserId, summary.ID, len(pbTodos))
	}()

	return &aibot.SummarizeResp{
		Status: "processing",
	}, nil
}

func (s *ConversationToolServer) GetConvSummaries(ctx context.Context, req *aibot.GetConvSummariesReq) (*aibot.GetConvSummariesResp, error) {
	summaryRepo := repo.NewConvSummaryRepo(s.svcCtx.DB)
	summaries, err := summaryRepo.FindByConv(ctx, req.ConvId, int(req.Limit))
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "查询总结记录失败", err)
	}

	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	items := make([]*aibot.SummarizeResp, 0, len(summaries))
	for _, s := range summaries {
		todos, _ := todoRepo.FindBySummary(ctx, s.ID)
		pbTodos := make([]*aibot.TodoItem, len(todos))
		for i, t := range todos {
			pbTodos[i] = &aibot.TodoItem{
				Id:        t.ID,
				SummaryId: t.SummaryID,
				ConvId:    t.ConvID,
				Content:   t.Content,
				Done:      t.Done,
				CreatedAt: t.CreatedAt.Unix(),
				UpdatedAt: t.UpdatedAt.Unix(),
			}
		}
		items = append(items, &aibot.SummarizeResp{
			SummaryId:     s.ID,
			Summary:       s.Summary,
			Todos:         pbTodos,
			TotalMessages: int32(s.MessageCount),
			CreatedAt:     s.CreatedAt.Unix(),
			Status:        "completed",
		})
	}
	return &aibot.GetConvSummariesResp{Items: items}, nil
}

func (s *ConversationToolServer) CreateTodo(ctx context.Context, req *aibot.CreateTodoReq) (*aibot.TodoItem, error) {
	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	t := &repo.SummaryTodo{
		SummaryID: req.SummaryId,
		ConvID:    req.ConvId,
		Content:   req.Content,
	}
	if err := todoRepo.Create(ctx, t); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "创建待办失败", err)
	}
	return &aibot.TodoItem{
		Id:        t.ID,
		SummaryId: t.SummaryID,
		ConvId:    t.ConvID,
		Content:   t.Content,
		Done:      t.Done,
		CreatedAt: t.CreatedAt.Unix(),
		UpdatedAt: t.UpdatedAt.Unix(),
	}, nil
}

func (s *ConversationToolServer) UpdateTodo(ctx context.Context, req *aibot.UpdateTodoReq) (*emptypb.Empty, error) {
	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	if err := todoRepo.Update(ctx, req.TodoId, req.Content, req.Done); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "更新待办失败", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *ConversationToolServer) DeleteTodo(ctx context.Context, req *aibot.DeleteTodoReq) (*emptypb.Empty, error) {
	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	if err := todoRepo.Delete(ctx, req.TodoId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "删除待办失败", err)
	}
	return &emptypb.Empty{}, nil
}

// GenerateReplyCandidates generates reply suggestions asynchronously.
// The LLM call runs in a background goroutine and pushes the complete result
// via WebSocket (type: conv.reply_candidates.done).
func (s *ConversationToolServer) GenerateReplyCandidates(ctx context.Context, req *aibot.ReplyCandidatesReq) (*aibot.ReplyCandidatesResp, error) {
	input, err := s.getLLMInput(ctx, req.UserId, req.ConvId)
	if err != nil {
		return nil, err
	}

	msgClient := client.NewMessageClient(s.svcCtx.MessageSvcConn)
	msgs, err := msgClient.GetRecentMessages(ctx, req.ConvId, req.UserId, 15)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取上下文消息失败", err)
	}

	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(fmt.Sprintf("[user_%d]: %s\n", m.SenderID, m.Content))
	}
	contextText := b.String()

	go func() {
		bgCtx := context.Background()
		candidates, llmErr := convtool.GenerateReplyCandidates(bgCtx, input, contextText)
		if llmErr != nil {
			s.svcCtx.Logger.Errorf("async reply candidates failed: conv=%d user=%d err=%v", req.ConvId, req.UserId, llmErr)
			s.pushAsyncResult(req.UserId, "conv.reply_candidates.failed", map[string]any{
				"conv_id": strconv.FormatInt(req.ConvId, 10),
				"error":   llmErr.Error(),
			})
			return
		}

		s.pushAsyncResult(req.UserId, "conv.reply_candidates.done", map[string]any{
			"conv_id":    strconv.FormatInt(req.ConvId, 10),
			"candidates": candidates,
		})

		s.svcCtx.Logger.Infof("async reply candidates done: conv=%d user=%d count=%d",
			req.ConvId, req.UserId, len(candidates))
	}()

	return &aibot.ReplyCandidatesResp{
		Status: "processing",
	}, nil
}

// TranslateMessage translates text asynchronously.
// It returns immediately with status "processing". The actual LLM call runs in a
// background goroutine and pushes the result via WebSocket (type: conv.translate.done).
func (s *ConversationToolServer) TranslateMessage(ctx context.Context, req *aibot.TranslateMessageReq) (*aibot.TranslateMessageResp, error) {
	userID := s.getUserID(ctx)
	input, err := s.getLLMInput(ctx, userID, 0)
	if err != nil {
		return nil, err
	}

	text := req.Text
	targetLang := req.TargetLang
	msgID := req.MsgId

	// Spawn async processing
	go func() {
		bgCtx := context.Background()
		result, llmErr := convtool.Translate(bgCtx, input, text, targetLang)
		if llmErr != nil {
			s.svcCtx.Logger.Errorf("async translate failed: user=%d msg=%d err=%v", userID, msgID, llmErr)
			s.pushAsyncResult(userID, "conv.translate.failed", map[string]any{
				"msg_id": strconv.FormatInt(msgID, 10),
				"error":  llmErr.Error(),
			})
			return
		}

		s.pushAsyncResult(userID, "conv.translate.done", map[string]any{
			"msg_id":          strconv.FormatInt(msgID, 10),
			"translated_text": result.TranslatedText,
			"detected_lang":   result.DetectedLang,
		})

		s.svcCtx.Logger.Infof("async translate done: user=%d msg=%d target=%s", userID, msgID, targetLang)
	}()

	return &aibot.TranslateMessageResp{
		Status: "processing",
	}, nil
}
