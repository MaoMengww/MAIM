package consts

// ---- Kafka topics ----

const (
	KafkaTopicMessageCreated          = "message.created"
	KafkaTopicMessageRecalled         = "message.recalled"
	KafkaTopicConversationReadUpdated = "conversation.read.updated"
	KafkaTopicMessageEdited           = "message.edited"
	KafkaTopicMessageDeleted          = "message.deleted"
	KafkaTopicConvBotAdded            = "conversation.bot.added"
	KafkaTopicMessageCreatedDLQ       = "message.created.dlq"
)

// ---- DLQ ----

const (
	DLQSuffix     = ".dlq"
	DLQTimeoutSec = 5
)

// ---- Elasticsearch ----

const (
	ESIndexMessages = "messages_v2"
)
