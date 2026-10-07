package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/bot-service/internal/client"
	"github.com/maomeng/aim/app/bot-service/internal/convtool"
	"github.com/maomeng/aim/app/bot-service/internal/graph"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

type ConversationToolServer struct {
	svcCtx *svc.ServiceContext
}

func NewConversationToolServer(svcCtx *svc.ServiceContext) *ConversationToolServer {
	return &ConversationToolServer{svcCtx: svcCtx}
}

func runtimeUserID(ctx context.Context) string {
	if id, ok := ctx.Value(interceptor.ContextKeyUserID).(string); ok && identity.Validate(id) == nil {
		return id
	}
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get(consts.MetadataKeyUserID)
	if len(ids) == 1 && identity.Validate(ids[0]) == nil {
		return ids[0]
	}
	return ""
}

func userCallContext(ctx context.Context, userID string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(consts.MetadataKeyUserID, userID)
	return metadata.NewOutgoingContext(ctx, md)
}

func (s *ConversationToolServer) requireConversation(ctx context.Context, userID, convID string) error {
	if identity.Validate(userID) != nil {
		return errors.New(errors.CodeUnauthorized, "missing authenticated user")
	}
	if identity.Validate(convID) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid conversation identity")
	}
	cli := msgpb.NewMessageServiceClient(s.svcCtx.MessageSvcConn.Conn())
	_, err := cli.GetMembers(userCallContext(ctx, userID), &msgpb.GetMembersReq{
		ConversationId: convID,
		UserId:         &userID,
		Pagination:     &commonpb.Pagination{Page: 1, PageSize: 1},
	})
	return err
}

func (s *ConversationToolServer) getLLMInput(ctx context.Context, userID, convID string) (*convtool.Input, error) {
	caller := runtimeUserID(ctx)
	if caller == "" {
		return nil, errors.New(errors.CodeUnauthorized, "missing authenticated user")
	}
	if caller != userID {
		return nil, errors.New(errors.CodeForbidden, "cannot operate as another user")
	}
	if convID != "" {
		if err := s.requireConversation(ctx, caller, convID); err != nil {
			return nil, err
		}
	}
	userClient := userpb.NewUserServiceClient(s.svcCtx.UserServiceConn.Conn())
	settings, err := userClient.GetSettings(userCallContext(ctx, caller), &commonpb.Empty{})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取用户设置失败", err)
	}
	if settings.AiModelId == nil {
		return nil, errors.New(errors.CodeInvalidParam, "请先在设置-个人资料中配置AI助手模型")
	}
	llm := client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn)
	return &convtool.Input{
		LLMClient: llm,
		ChatModel: llm.NewEinoChatModel(*settings.AiModelId, settings.AiModelName, &caller),
		UserID:    caller,
		ConvID:    convID,
	}, nil
}

// Async results are account-scoped; membership is checked again after model work.
func (s *ConversationToolServer) pushAsyncResult(userID, convID, eventType string, data map[string]any) {
	ctx := context.Background()
	if convID != "" {
		if err := s.requireConversation(ctx, userID, convID); err != nil {
			s.svcCtx.Logger.Errorf("async result permission denied: conv=%s user=%s err=%v", convID, userID, err)
			return
		}
	}
	msg := map[string]any{"type": eventType}
	for k, v := range data {
		msg[k] = v
	}
	b, err := json.Marshal(msg)
	if err == nil {
		err = model.ValidateEntityJSON(b)
	}
	if err != nil {
		s.svcCtx.Logger.Errorf("async result encode failed: type=%s err=%v", eventType, err)
		return
	}
	if err := s.svcCtx.DeliveryPublisher.Publish(ctx, userID, delivery.Intent{UserIDs: []string{userID}, Payload: b}); err != nil {
		s.svcCtx.Logger.Errorf("async result push failed: type=%s user=%s err=%v", eventType, userID, err)
	}
}

func (s *ConversationToolServer) SummarizeConversation(ctx context.Context, req *botpb.SummarizeReq) (*botpb.SummarizeResp, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.SummarizeConversation(forwardRuntimeContext(ctx), req)
	}
	input, err := s.getLLMInput(ctx, req.UserId, req.ConvId)
	if err != nil {
		return nil, err
	}
	maxCount := 100
	switch r := req.Range.(type) {
	case *botpb.SummarizeReq_LastMessageCount:
		maxCount = int(r.LastMessageCount)
	case *botpb.SummarizeReq_All:
		maxCount = 1000
	}
	allMsgs, err := client.NewMessageClient(s.svcCtx.MessageSvcConn).GetAllMessages(ctx, req.ConvId, req.UserId, maxCount)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取消息失败", err)
	}
	if len(allMsgs) == 0 {
		return nil, errors.New(errors.CodeInvalidParam, "没有可总结的消息")
	}
	msgs := make([]convtool.Message, len(allMsgs))
	for i, m := range allMsgs {
		msgs[len(allMsgs)-1-i] = convtool.Message{MsgID: m.MsgID, SenderID: m.SenderID, SenderName: m.SenderName, Content: m.Content, MsgType: m.MsgType, Seq: m.Seq}
	}
	messageText := convtool.BuildMessageText(msgs)
	go func() {
		bgCtx := context.Background()
		fail := func(err error) {
			s.svcCtx.Logger.Errorf("async summarize failed: conv=%s user=%s err=%v", req.ConvId, req.UserId, err)
			s.pushAsyncResult(req.UserId, req.ConvId, "conv.summarize.failed", map[string]any{"conv_id": req.ConvId, "error": err.Error()})
		}
		result, err := convtool.Summarize(bgCtx, input, messageText, len(msgs))
		if err != nil {
			fail(err)
			return
		}
		if err := s.requireConversation(bgCtx, req.UserId, req.ConvId); err != nil {
			return
		}
		summary := &repo.ConvSummary{ConvID: req.ConvId, UserID: req.UserId, RangeType: fmt.Sprintf("count_%d", maxCount), MessageCount: result.TotalMessages, Summary: result.Summary}
		var todos []map[string]any
		err = s.svcCtx.DB.WithContext(bgCtx).Transaction(func(tx *gorm.DB) error {
			db := &database.DB{DB: tx}
			if err := repo.NewConvSummaryRepo(db).Create(bgCtx, summary); err != nil {
				return err
			}
			todoRepo := repo.NewSummaryTodoRepo(db)
			for _, text := range result.Todos {
				t := &repo.SummaryTodo{SummaryID: &summary.ID, ConvID: req.ConvId, Content: text}
				if err := todoRepo.Create(bgCtx, t); err != nil {
					return err
				}
				todos = append(todos, map[string]any{"id": t.ID, "summary_id": t.SummaryID, "conv_id": t.ConvID, "content": t.Content, "done": t.Done, "created_at": t.CreatedAt.Unix()})
			}
			return nil
		})
		if err != nil {
			fail(err)
			return
		}
		s.pushAsyncResult(req.UserId, req.ConvId, "conv.summarize.done", map[string]any{"conv_id": req.ConvId, "summary_id": summary.ID, "summary": summary.Summary, "todos": todos, "total_messages": result.TotalMessages, "created_at": summary.CreatedAt.Unix()})
	}()
	return &botpb.SummarizeResp{Status: "processing"}, nil
}

func (s *ConversationToolServer) GetConvSummaries(ctx context.Context, req *botpb.GetConvSummariesReq) (*botpb.GetConvSummariesResp, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.GetConvSummaries(forwardRuntimeContext(ctx), req)
	}
	if err := s.requireConversation(ctx, runtimeUserID(ctx), req.ConvId); err != nil {
		return nil, err
	}
	summaries, err := repo.NewConvSummaryRepo(s.svcCtx.DB).FindByConv(ctx, req.ConvId, int(req.Limit))
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "查询总结记录失败", err)
	}
	todoRepo := repo.NewSummaryTodoRepo(s.svcCtx.DB)
	items := make([]*botpb.SummarizeResp, 0, len(summaries))
	for _, summary := range summaries {
		todos, err := todoRepo.FindBySummary(ctx, summary.ID)
		if err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "查询待办失败", err)
		}
		pbTodos := make([]*botpb.TodoItem, len(todos))
		for i := range todos {
			pbTodos[i] = todoToProto(&todos[i])
		}
		items = append(items, &botpb.SummarizeResp{SummaryId: &summary.ID, Summary: summary.Summary, Todos: pbTodos, TotalMessages: int32(summary.MessageCount), CreatedAt: summary.CreatedAt.Unix(), Status: "completed"})
	}
	standalone, err := todoRepo.FindStandaloneByConv(ctx, req.ConvId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "查询待办失败", err)
	}
	pbStandalone := make([]*botpb.TodoItem, len(standalone))
	for i := range standalone {
		pbStandalone[i] = todoToProto(&standalone[i])
	}
	return &botpb.GetConvSummariesResp{Items: items, StandaloneTodos: pbStandalone}, nil
}

func todoToProto(t *repo.SummaryTodo) *botpb.TodoItem {
	return &botpb.TodoItem{Id: t.ID, SummaryId: t.SummaryID, ConvId: t.ConvID, Content: t.Content, Done: t.Done, CreatedAt: t.CreatedAt.Unix(), UpdatedAt: t.UpdatedAt.Unix()}
}

func (s *ConversationToolServer) CreateTodo(ctx context.Context, req *botpb.CreateTodoReq) (*botpb.TodoItem, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.CreateTodo(forwardRuntimeContext(ctx), req)
	}
	if err := s.requireConversation(ctx, runtimeUserID(ctx), req.ConvId); err != nil {
		return nil, err
	}
	if req.SummaryId != nil {
		if identity.Validate(*req.SummaryId) != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid summary identity")
		}
		summary, err := repo.NewConvSummaryRepo(s.svcCtx.DB).Get(ctx, *req.SummaryId)
		if err != nil {
			return nil, errors.Wrap(errors.CodeNotFound, "summary not found", err)
		}
		if summary.ConvID != req.ConvId {
			return nil, errors.New(errors.CodeForbidden, "summary belongs to another conversation")
		}
	}
	t := &repo.SummaryTodo{SummaryID: req.SummaryId, ConvID: req.ConvId, Content: req.Content}
	if err := repo.NewSummaryTodoRepo(s.svcCtx.DB).Create(ctx, t); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "创建待办失败", err)
	}
	return todoToProto(t), nil
}

func (s *ConversationToolServer) requireTodo(ctx context.Context, todoID, convID string) error {
	if identity.Validate(todoID) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid todo identity")
	}
	if err := s.requireConversation(ctx, runtimeUserID(ctx), convID); err != nil {
		return err
	}
	todo, err := repo.NewSummaryTodoRepo(s.svcCtx.DB).Get(ctx, todoID)
	if err != nil {
		return errors.Wrap(errors.CodeNotFound, "todo not found", err)
	}
	if todo.ConvID != convID {
		return errors.New(errors.CodeForbidden, "todo belongs to another conversation")
	}
	return nil
}

func (s *ConversationToolServer) UpdateTodo(ctx context.Context, req *botpb.UpdateTodoReq) (*emptypb.Empty, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.UpdateTodo(forwardRuntimeContext(ctx), req)
	}
	if err := s.requireTodo(ctx, req.TodoId, req.ConvId); err != nil {
		return nil, err
	}
	if err := repo.NewSummaryTodoRepo(s.svcCtx.DB).Update(ctx, req.TodoId, req.ConvId, req.Content, req.Done); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "更新待办失败", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *ConversationToolServer) DeleteTodo(ctx context.Context, req *botpb.DeleteTodoReq) (*emptypb.Empty, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.DeleteTodo(forwardRuntimeContext(ctx), req)
	}
	if err := s.requireTodo(ctx, req.TodoId, req.ConvId); err != nil {
		return nil, err
	}
	if err := repo.NewSummaryTodoRepo(s.svcCtx.DB).Delete(ctx, req.TodoId, req.ConvId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "删除待办失败", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *ConversationToolServer) GenerateReplyCandidates(ctx context.Context, req *botpb.ReplyCandidatesReq) (*botpb.ReplyCandidatesResp, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.GenerateReplyCandidates(forwardRuntimeContext(ctx), req)
	}
	input, err := s.getLLMInput(ctx, req.UserId, req.ConvId)
	if err != nil {
		return nil, err
	}
	msgs, err := client.NewMessageClient(s.svcCtx.MessageSvcConn).GetRecentMessages(ctx, req.ConvId, req.UserId, 15)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "获取上下文消息失败", err)
	}
	contextText := graph.FormatHistory(msgs)
	go func() {
		candidates, err := convtool.GenerateReplyCandidates(context.Background(), input, contextText)
		if err != nil {
			s.pushAsyncResult(req.UserId, req.ConvId, "conv.reply_candidates.failed", map[string]any{"conv_id": req.ConvId, "error": err.Error()})
			return
		}
		s.pushAsyncResult(req.UserId, req.ConvId, "conv.reply_candidates.done", map[string]any{"conv_id": req.ConvId, "candidates": candidates})
	}()
	return &botpb.ReplyCandidatesResp{Status: "processing"}, nil
}

func (s *ConversationToolServer) TranslateMessage(ctx context.Context, req *botpb.TranslateMessageReq) (*botpb.TranslateMessageResp, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.TranslateMessage(forwardRuntimeContext(ctx), req)
	}
	userID := runtimeUserID(ctx)
	convID := ""
	if req.MsgId != nil {
		if identity.Validate(*req.MsgId) != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid message identity")
		}
		cli := msgpb.NewMessageServiceClient(s.svcCtx.MessageSvcConn.Conn())
		resp, err := cli.GetMessageByID(userCallContext(ctx, userID), &msgpb.GetMessageByIDReq{MessageId: *req.MsgId})
		if err != nil {
			return nil, err
		}
		convID = resp.Message.ConversationId
	}
	input, err := s.getLLMInput(ctx, userID, convID)
	if err != nil {
		return nil, err
	}
	go func() {
		result, err := convtool.Translate(context.Background(), input, req.Text, req.TargetLang)
		data := map[string]any{}
		if req.MsgId != nil {
			data["msg_id"] = *req.MsgId
		}
		if err != nil {
			data["error"] = err.Error()
			s.pushAsyncResult(userID, convID, "conv.translate.failed", data)
			return
		}
		data["translated_text"] = result.TranslatedText
		data["detected_lang"] = result.DetectedLang
		s.pushAsyncResult(userID, convID, "conv.translate.done", data)
	}()
	return &botpb.TranslateMessageResp{Status: "processing"}, nil
}
