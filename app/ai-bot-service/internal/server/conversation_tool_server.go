package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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

func (s *ConversationToolServer) SummarizeConversation(ctx context.Context, req *aibot.SummarizeReq) (*aibot.SummarizeResp, error) {
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

	// Reverse to chronological order
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

	// Handle time range filtering
	if tr, ok := req.Range.(*aibot.SummarizeReq_TimeRange); ok {
		startTime := tr.TimeRange.StartTime
		endTime := tr.TimeRange.EndTime
		if endTime == 0 {
			endTime = time.Now().Unix()
		}
		_ = startTime
		_ = endTime
		// Time-based filtering would require message timestamp - use all messages for now
	}

	messageText := convtool.BuildMessageText(msgs)
	result, err := convtool.Summarize(ctx, input, messageText, len(msgs))
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "总结失败", err)
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
	if err := summaryRepo.Create(ctx, summary); err != nil {
		s.svcCtx.Logger.WithContext(ctx).Errorf("save summary failed: %v", err)
	}

	// Persist todos
	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	pbTodos := make([]*aibot.TodoItem, 0, len(result.Todos))
	for _, todoText := range result.Todos {
		t := &repo.SummaryTodo{
			SummaryID: summary.ID,
			ConvID:    req.ConvId,
			Content:   todoText,
		}
		if err := todoRepo.Create(ctx, t); err != nil {
			s.svcCtx.Logger.WithContext(ctx).Errorf("save todo failed: %v", err)
			continue
		}
		pbTodos = append(pbTodos, &aibot.TodoItem{
			Id:        t.ID,
			SummaryId: t.SummaryID,
			ConvId:    t.ConvID,
			Content:   t.Content,
			Done:      t.Done,
			CreatedAt: t.CreatedAt.Unix(),
			UpdatedAt: t.UpdatedAt.Unix(),
		})
	}

	return &aibot.SummarizeResp{
		SummaryId:     summary.ID,
		Summary:       result.Summary,
		Todos:         pbTodos,
		TotalMessages: int32(result.TotalMessages),
		CreatedAt:     summary.CreatedAt.Unix(),
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

	candidates, err := convtool.GenerateReplyCandidates(ctx, input, b.String())
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "生成回复候选失败", err)
	}

	return &aibot.ReplyCandidatesResp{Candidates: candidates}, nil
}

func (s *ConversationToolServer) TranslateMessage(ctx context.Context, req *aibot.TranslateMessageReq) (*aibot.TranslateMessageResp, error) {
	userID := s.getUserID(ctx)
	input, err := s.getLLMInput(ctx, userID, 0)
	if err != nil {
		return nil, err
	}

	result, err := convtool.Translate(ctx, input, req.Text, req.TargetLang)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "翻译失败", err)
	}

	return &aibot.TranslateMessageResp{
		TranslatedText: result.TranslatedText,
		DetectedLang:   result.DetectedLang,
	}, nil
}
