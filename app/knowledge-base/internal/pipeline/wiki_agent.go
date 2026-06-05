package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	llmgatewaypb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/snowflake"
)

// WikiEinoAgent wraps the eino ReAct agent for wiki queries.
type WikiEinoAgent struct {
	tools     []tool.BaseTool
	llmClient llmgatewaypb.LLMGatewayClient
	snowflake *snowflake.Node
}

// NewWikiEinoAgent creates a new WikiEinoAgent with all wiki tools.
func NewWikiEinoAgent(
	wikiRepo domain.WikiPageRepo,
	docRepo domain.DocumentRepo,
	fileStore domain.FileStore,
	llmClient llmgatewaypb.LLMGatewayClient,
	snow *snowflake.Node,
) *WikiEinoAgent {
	deps := &WikiToolDeps{
		WikiRepo:  wikiRepo,
		DocRepo:   docRepo,
		FileStore: fileStore,
		Snowflake: snow,
	}
	return &WikiEinoAgent{
		tools:     NewWikiTools(deps),
		llmClient: llmClient,
		snowflake: snow,
	}
}

// Query runs a ReAct agent query against the wiki knowledge base.
func (a *WikiEinoAgent) Query(ctx context.Context, kbID int64, query string, modelID int64, modelName string, ownerID int64, history string) (*WikiAgentOutput, error) {
	chatModel := newEinoChatModel(a.llmClient, modelID, ownerID)

	// Build system prompt
	systemPrompt := a.buildSystemPrompt()

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: a.tools,
		},
		MessageModifier: func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
			result := make([]*schema.Message, 0, len(msgs)+2)
			result = append(result, &schema.Message{
				Role:    schema.System,
				Content: systemPrompt,
			})
			if history != "" {
				result = append(result, &schema.Message{
					Role:    schema.User,
					Content: "Current conversation history:\\n" + history,
				})
			}
			result = append(result, msgs...)
			return result
		},
		MaxStep: 12,
	})
	if err != nil {
		return nil, err
	}

	msg, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: query},
	})
	if err != nil {
		return nil, err
	}

	refs := extractRefsFromContent(msg.Content)
	return &WikiAgentOutput{
		Answer:     msg.Content,
		References: refs,
	}, nil
}

func (a *WikiEinoAgent) buildSystemPrompt() string {
	return `You are a rigorous Wiki knowledge base assistant, providing reference information based on the Wiki knowledge base.

## Response Requirements

1. **Language:** Please respond in Chinese (except for proper nouns).

2. **Never guess:** Only answer based on content retrieved from the knowledge base. If no relevant information is found, clearly inform the user.

3. **Wiki first:** For facts that can be covered by the wiki, you must search and read the wiki first before falling back to general reasoning.

4. **Must read:** wiki_search only returns summaries. Do not write answers based solely on search results; you must call wiki_read_page to get the actual content.

5. **Cite sources:** You must indicate information sources:
   - From wiki pages → [[slug|display name]]
   - From source documents → [doc:doc_id]

6. **Strict ontology:** Do not fabricate wiki slugs. Any [[slug|display name]] must be a valid verified page.

7. **Knowledge recording:** If the user's question involves knowledge not yet recorded in the wiki, use wiki_write_page to record it. Search first to confirm it does not already exist before writing.

## Tool Usage

- Do not know what to look for → start with wiki_read_index for an overview
- Know the specific page → wiki_read_page for detailed content
- Search results insufficient → wiki_read_source_doc to trace back to source documents for more details
- Found an error on a page → wiki_flag_issue to flag it

At the end of your answer, recommend 2-3 related pages for further reading:
> Further reading: [[entity/transformer|Transformer]]`
}

// extractRefsFromContent extracts [[slug]] references from markdown content
func extractRefsFromContent(content string) []string {
	var refs []string
	seen := make(map[string]bool)
	for {
		start := strings.Index(content, "[[")
		if start < 0 {
			break
		}
		end := strings.Index(content[start:], "]]")
		if end < 0 {
			break
		}
		inner := content[start+2 : start+end]
		// Handle [[slug|title]] -> extract slug
		if pipeIdx := strings.Index(inner, "|"); pipeIdx >= 0 {
			inner = inner[:pipeIdx]
		}
		inner = strings.TrimSpace(inner)
		if inner != "" && !seen[inner] {
			seen[inner] = true
			refs = append(refs, inner)
		}
		content = content[start+end+2:]
	}
	return refs
}

// ========================================
// einoChatModel — minimal eino ChatModel wrapper for llm-gateway gRPC
// ========================================

// einoChatModel wraps llm-gateway gRPC as an eino ToolCallingChatModel.
type einoChatModel struct {
	client   llmgatewaypb.LLMGatewayClient
	modelID  int64
	ownerID  int64
	toolInfo []*schema.ToolInfo
}

func newEinoChatModel(cli llmgatewaypb.LLMGatewayClient, modelID, ownerID int64) *einoChatModel {
	return &einoChatModel{client: cli, modelID: modelID, ownerID: ownerID}
}

func (m *einoChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req := &llmgatewaypb.ChatReq{
		ModelId:  m.modelID,
		OwnerId:  m.ownerID,
		Messages: einoToProtoMessages(messages),
	}
	if len(m.toolInfo) > 0 {
		req.Tools = toolInfoToProto(m.toolInfo)
	}
	resp, err := m.client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return einoToSchemaMessage(resp), nil
}

func (m *einoChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req := &llmgatewaypb.ChatReq{
		ModelId:  m.modelID,
		OwnerId:  m.ownerID,
		Messages: einoToProtoMessages(messages),
	}
	if len(m.toolInfo) > 0 {
		req.Tools = toolInfoToProto(m.toolInfo)
	}
	stream, err := m.client.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}
	reader, writer := schema.Pipe[*schema.Message](0)
	go func() {
		defer writer.Close()
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = writer.Send(nil, err)
				return
			}
			msg := einoStreamChunkToSchema(chunk)
			if msg != nil {
				_ = writer.Send(msg, nil)
			}
		}
	}()
	return reader, nil
}

func (m *einoChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.toolInfo = append(m.toolInfo, tools...)
	return m, nil
}

func (m *einoChatModel) BindTools(tools []*schema.ToolInfo) error {
	m.toolInfo = append(m.toolInfo, tools...)
	return nil
}

// toolInfoToProto converts eino ToolInfo definitions to llm-gateway ToolDef proto slice.
func toolInfoToProto(tis []*schema.ToolInfo) []*llmgatewaypb.ToolDef {
	protos := make([]*llmgatewaypb.ToolDef, 0, len(tis))
	for _, ti := range tis {
		t := &llmgatewaypb.ToolDef{
			Name:        ti.Name,
			Description: ti.Desc,
		}
		if ti.ParamsOneOf != nil {
			js, err := ti.ParamsOneOf.ToJSONSchema()
			if err == nil && js != nil {
				if b, err := json.Marshal(js); err == nil && string(b) != "{}" {
					t.Parameters = b
				}
			}
		}
		protos = append(protos, t)
	}
	return protos
}

// -- helpers ------------------------------------------------------------

func einoToProtoMessages(msgs []*schema.Message) []*llmgatewaypb.Message {
	out := make([]*llmgatewaypb.Message, 0, len(msgs))
	for _, msg := range msgs {
		p := &llmgatewaypb.Message{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallId: msg.ToolCallID,
		}
		for _, tc := range msg.ToolCalls {
			p.ToolCalls = append(p.ToolCalls, &llmgatewaypb.ToolCall{
				Id:   tc.ID,
				Type: tc.Type,
				Function: &llmgatewaypb.ToolCall_Function{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, p)
	}
	return out
}

func einoToSchemaMessage(resp *llmgatewaypb.ChatResp) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant}
	if len(resp.Choices) > 0 {
		msg.Content = resp.Choices[0].Message.Content
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   tc.Id,
				Type: tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}
	if resp.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     int(resp.Usage.PromptTokens),
				CompletionTokens: int(resp.Usage.CompletionTokens),
			},
		}
	}
	return msg
}

func einoStreamChunkToSchema(chunk *llmgatewaypb.ChatStreamChunk) *schema.Message {
	if len(chunk.Choices) == 0 {
		return nil
	}
	c := chunk.Choices[0]
	msg := &schema.Message{Role: schema.Assistant, Content: c.Delta.Content}
	for _, tc := range c.Delta.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.Id,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return msg
}
