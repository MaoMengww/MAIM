package llm

import (
	"github.com/cloudwego/eino/schema"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

func ProtoToEinoMessages(pbMsgs []*pb.Message) []*schema.Message {
	msgs := make([]*schema.Message, len(pbMsgs))
	for i, m := range pbMsgs {
		sm := &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallId}
		for _, tc := range m.ToolCalls {
			sm.ToolCalls = append(sm.ToolCalls, schema.ToolCall{
				ID: tc.Id, Type: tc.Type,
				Function: schema.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
		msgs[i] = sm
	}
	return msgs
}

func EinoMessageToProto(sm *schema.Message) *pb.Message {
	msg := &pb.Message{Role: string(sm.Role), Content: sm.Content, Name: sm.Name, ToolCallId: sm.ToolCallID}
	for _, tc := range sm.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, &pb.ToolCall{
			Id: tc.ID, Type: tc.Type,
			Function: &pb.ToolCall_Function{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		})
	}
	return msg
}

func SchemaTokenUsageToProto(tu *schema.TokenUsage) *pb.UsageInfo {
	if tu == nil {
		return nil
	}
	return &pb.UsageInfo{PromptTokens: int32(tu.PromptTokens), CompletionTokens: int32(tu.CompletionTokens), TotalTokens: int32(tu.TotalTokens)}
}
