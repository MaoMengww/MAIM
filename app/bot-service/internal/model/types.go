package model

import (
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/pkg/identity"
)

// BotEvent represents an incoming Kafka event for a bot.
type BotEvent struct {
	EventType        string          `json:"event_type"`
	BotID            string          `json:"bot_id"`
	ConvID           string          `json:"conv_id"`
	Message          *EventMessage   `json:"message"`
	Sender           *EventSender    `json:"sender"`
	MentionedUserIDs []string        `json:"mentioned_user_ids"`
	BotConfig        *EventBotConfig `json:"bot_config"`
}

// EventMessage is the message part of a bot event.
type EventMessage struct {
	MsgID        string  `json:"msg_id"`
	Text         string  `json:"text"`
	MsgType      int32   `json:"msg_type"`
	CreatedAt    int64   `json:"created_at"`
	ReplyToMsgID *string `json:"reply_to_msg_id,omitempty"`
}

// EventSender is the sender part of a bot event.
type EventSender struct {
	UserID   *string `json:"user_id,omitempty"`
	Username string  `json:"username"`
	Language string  `json:"language"`
}

// EventBotConfig is the bot config snippet carried in a bot event.
type EventBotConfig struct {
	ResponseTriggers []string `json:"response_triggers"`
	UsePlatformModel bool     `json:"use_platform_model"`
}

// StreamChunk is used for streaming output.
type StreamChunk struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name"`
	MessageID string `json:"message_id,omitempty"`
	ConvID    string `json:"conv_id"`
}

// ValidateEntityJSON validates the entity references in bot configuration and payloads.
// Template names, provider tool-call IDs and stream IDs are not entity references.
func ValidateEntityJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return validateEntityJSONValue(value)
}

func validateEntityJSONValue(value any) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "user_id", "userId", "owner_id", "ownerId", "bot_id", "botId", "sender_id", "senderId",
				"conversation_id", "conversationId", "conv_id", "convId", "message_id", "messageId",
				"msg_id", "msgId", "reply_to_id", "replyToId", "reply_to_msg_id", "replyToMsgId",
				"model_id", "modelId", "memory_model_id", "memoryModelId",
				"memory_embedding_model_id", "memoryEmbeddingModelId", "mcp_server_id", "mcpServerId",
				"mcp_tool_id", "mcpToolId", "summary_id", "summaryId", "todo_id", "todoId",
				"official_template_id", "officialTemplateId", "knowledge_base_id", "knowledgeBaseId", "kb_id", "kbId",
				"doc_id", "docId", "chunk_id", "chunkId":
				if child != nil {
					id, ok := child.(string)
					if !ok || identity.Validate(id) != nil {
						return fmt.Errorf("invalid entity reference %s", key)
					}
				}
			case "user_ids", "userIds", "bot_ids", "botIds", "model_ids", "modelIds",
				"mcp_server_ids", "mcpServerIds", "knowledge_base_ids", "knowledgeBaseIds",
				"mentioned_user_ids", "mentionedUserIds", "mention_user_ids", "mentionUserIds", "kb_ids", "kbIds":
				if child == nil {
					break
				}
				ids, ok := child.([]any)
				if !ok {
					return fmt.Errorf("invalid entity references %s", key)
				}
				for _, value := range ids {
					id, ok := value.(string)
					if !ok || identity.Validate(id) != nil {
						return fmt.Errorf("invalid entity reference %s", key)
					}
				}
			}
			if err := validateEntityJSONValue(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := validateEntityJSONValue(child); err != nil {
				return err
			}
		}
	}
	return nil
}
