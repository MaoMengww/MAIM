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
	EventSubscribePresence   = "presence.subscribe"
	EventUnsubscribePresence = "presence.unsubscribe"
	EventTyping              = "typing"
	EventTypingStop          = "typing.stop"
	EventAck                 = "ack"

	// Server to Client
	EventPong             = "pong"
	EventMessageNew       = "message.new"
	EventMessageRecalled  = "message.recalled"
	EventMessageEdited    = "message.edited"
	EventPresence         = "presence.state"
	EventReadSync         = "read_sync"
	EventTypingNotify     = "typing"
	EventTypingStopNotify = "typing.stop"
	EventUnreadCount      = "unread_count"
	EventReadReceipt      = "read_receipt"
	EventNotificationNew  = "notification.new"
)

// ---- WebSocket timing ----

const (
	TypingTTL = 3 * time.Second
)
