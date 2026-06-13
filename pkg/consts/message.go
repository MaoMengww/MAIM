package consts

// ---- Message types (aligned with protobuf) ----

const (
	MsgTypeUnspecified int32 = 0
	MsgTypeText        int32 = 1
	MsgTypeImage       int32 = 2
	MsgTypeFile        int32 = 3
	MsgTypeVideo       int32 = 4
	MsgTypeAudio       int32 = 5
	MsgTypeLocation    int32 = 6
	MsgTypeSystem      int32 = 7
	MsgTypeCustom      int32 = 8
	MsgTypeBot         int32 = 9
)

// ---- Message status ----

const (
	MsgStatusNormal    int32 = 1
	MsgStatusRecalled  int32 = 2
	MsgStatusEdited    int32 = 3
	MsgStatusStreaming int32 = 4
)

// ---- Friend request status ----

const (
	FriendReqUnspecified = 0
	FriendReqPending     = 1
	FriendReqAccepted    = 2
	FriendReqRejected    = 3
	FriendReqCancelled   = 4
)

// ---- Notification types ----

const (
	NotifTypeSystem = 1
	NotifTypeAudit  = 2
	NotifTypeBot    = 3
)

// ---- HTTP status codes ----

const (
	StatusCodeSuccess      = 0
	StatusCodeBadRequest   = 400
	StatusCodeUnauthorized = 401
	StatusCodeForbidden    = 403
	StatusCodeNotFound     = 404
	StatusCodeConflict     = 409
	StatusCodeInternal     = 500
	StatusCodeServiceDown  = 503
)

// ---- Message / Conversation limits ----

const (
	RecallWindowSec    = 120
	EditWindowSec      = 120
	MaxMemberCount     = 500
	MaxGroupNameLen    = 64
	MaxAliasLen        = 32
	MaxAnnouncementLen = 1024
	MaxFileSize        = 100 * 1024 * 1024 // 100 MB
)

// ---- Error codes ----

const (
	ErrCodeUnknown  = 1000
	ErrCodeInternal = 1006
)

// ---- Conversation action types ----

const (
	ConvActionMemberJoined = "member.joined"
	ConvActionMemberLeft   = "member.left"
)
