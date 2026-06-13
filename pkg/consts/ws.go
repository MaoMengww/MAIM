package consts

import "time"

// ---- Presence status ----

const (
	PresenceOnline  = "online"
	PresenceOffline = "offline"
)

// ---- WebSocket events ----

const (
	// Client to Server
	EventPing                = "ping"
	EventSubscribePresence   = "subscribe_presence"
	EventUnsubscribePresence = "unsubscribe_presence"
	EventTyping              = "typing"
	EventTypingStop          = "typing_stop"
	EventAck                 = "ack"

	// Server to Client
	EventPong             = "pong"
	EventMessageNew       = "message.new"
	EventMessageRecalled  = "message.recalled"
	EventMessageEdited    = "message.edited"
	EventPresence         = "presence"
	EventReadSync         = "read_sync"
	EventTypingNotify     = "typing"
	EventTypingStopNotify = "typing.stop"
	EventUnreadCount      = "unread_count"
	EventReadReceipt      = "read_receipt"
)

// ---- WebSocket timing ----

const (
	TypingTTL = 3 * time.Second
)
