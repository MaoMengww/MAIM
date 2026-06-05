package graph

import (
	"context"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/maomeng/aim/pkg/logx"
)

const tracerName = "ai-bot-service"

// NewOTelCallbackHandler creates an eino callbacks.Handler that provides OTel
// instrumentation for all graph components (lambda nodes, chat model, tools).
// Register with callbacks.AppendGlobalHandlers once at startup.
func NewOTelCallbackHandler(logger logx.Logger) callbacks.Handler {
	tracer := otel.Tracer(tracerName)

	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			var span trace.Span
			switch info.Component {
			case compose.ComponentOfGraph:
				ctx, span = tracer.Start(ctx, "Graph/"+info.Name)
			case compose.ComponentOfLambda:
				ctx, span = tracer.Start(ctx, "Lambda/"+info.Name)
			case components.ComponentOfChatModel:
				ctx, span = tracer.Start(ctx, "ChatModel/"+info.Name,
					trace.WithAttributes(
						attribute.String("model_name", info.Name),
					))
				if cbInput := model.ConvCallbackInput(input); cbInput != nil {
					span.SetAttributes(attribute.Int("message_count", len(cbInput.Messages)))
				}
			case compose.ComponentOfToolsNode:
				ctx, span = tracer.Start(ctx, "ToolsNode/"+info.Name)
			case components.ComponentOfTool:
				ctx, span = tracer.Start(ctx, "Tool/"+info.Name)
			default:
				ctx, span = tracer.Start(ctx, "Component/"+string(info.Component))
			}
			span.SetAttributes(
				attribute.String("component", string(info.Component)),
				attribute.String("component_name", info.Name))
			return context.WithValue(ctx, spanKey, span)
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if span, ok := ctx.Value(spanKey).(trace.Span); ok {
				// Record token usage for chat model
				if info.Component == components.ComponentOfChatModel {
					if cbOutput := model.ConvCallbackOutput(output); cbOutput != nil && cbOutput.TokenUsage != nil {
						span.SetAttributes(
							attribute.Int("input_tokens", cbOutput.TokenUsage.PromptTokens),
							attribute.Int("output_tokens", cbOutput.TokenUsage.CompletionTokens),
						)
					}
				}
				span.End()
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			if span, ok := ctx.Value(spanKey).(trace.Span); ok {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
			}
			logger.WithContext(ctx).Errorf("component %s/%s failed: %v", info.Component, info.Name, err)
			return ctx
		}).
		Build()
}

// spanKey is the context key for OTel spans.
type spanKeyType struct{}

var spanKey = spanKeyType{}
