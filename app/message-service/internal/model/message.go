package model

import (
	"database/sql/driver"
	"encoding/json"

	"github.com/maomeng/aim/pkg/consts"
	"time"
)

type Message struct {
	ID           int64       `gorm:"primaryKey;column:id" json:"id"`
	ConvID       int64       `gorm:"column:conv_id;index:idx_conv_seq" json:"conv_id"`
	SenderID     int64       `gorm:"column:sender_id" json:"sender_id"`
	SenderType   string      `gorm:"column:sender_type;default:user" json:"sender_type"`
	ClientMsgID  string      `gorm:"column:client_msg_id" json:"client_msg_id"`
	Seq          int64       `gorm:"column:seq" json:"seq"`
	MsgType      int32       `gorm:"column:msg_type" json:"msg_type"`
	Content      JSONContent `gorm:"column:content;type:jsonb" json:"content"`
	ReplyToMsgID int64       `gorm:"column:reply_to_msg_id" json:"reply_to_msg_id"`
	Status       int32       `gorm:"column:status;default:1" json:"status"`
	EditHistory  JSONArray   `gorm:"column:edit_history;type:jsonb" json:"edit_history"`
	EditCount    int32       `gorm:"column:edit_count" json:"edit_count"`
	CreatedAt    time.Time   `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time   `gorm:"column:updated_at" json:"updated_at"`
}

func (Message) TableName() string {
	return "messages"
}

type UserInbox struct {
	UserID      int64     `gorm:"primaryKey;column:user_id" json:"user_id"`
	ConvID      int64     `gorm:"primaryKey;column:conv_id" json:"conv_id"`
	MessageID   int64     `gorm:"column:message_id" json:"message_id"`
	Seq         int64     `gorm:"primaryKey;column:seq" json:"seq"`
	LastReadSeq int64     `gorm:"column:last_read_seq" json:"last_read_seq"`
	IsDeleted   bool      `gorm:"column:is_deleted" json:"is_deleted"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

func (UserInbox) TableName() string {
	return "user_inbox"
}

type Broadcast struct {
	ID            int64       `gorm:"primaryKey;column:id" json:"id"`
	SenderID      int64       `gorm:"column:sender_id" json:"sender_id"`
	Content       JSONContent `gorm:"column:content;type:jsonb" json:"content"`
	Scope         string      `gorm:"column:scope" json:"scope"`
	ScopeTargetID int64       `gorm:"column:scope_target_id" json:"scope_target_id"`
	CreatedAt     time.Time   `gorm:"column:created_at" json:"created_at"`
}

func (Broadcast) TableName() string {
	return "broadcasts"
}

type Sequence struct {
	ConvID     int64 `gorm:"primaryKey;column:conv_id" json:"conv_id"`
	CurrentSeq int64 `gorm:"column:current_seq" json:"current_seq"`
}

func (Sequence) TableName() string {
	return "sequences"
}

type JSONContent map[string]any

func (j JSONContent) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONContent) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

type JSONArray []map[string]any

// ---- Typed content structs ----

type TextContent struct {
	Text           string  `json:"text"`
	MentionUserIDs []int64 `json:"mention_user_ids"`
	MentionAll     bool    `json:"mention_all"`
}

type ImageContent struct {
	FileID       int64  `json:"file_id"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Width        int32  `json:"width"`
	Height       int32  `json:"height"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
}

type FileContent struct {
	FileID   int64  `json:"file_id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Ext      string `json:"ext"`
	MimeType string `json:"mime_type"`
}

type VideoContent struct {
	FileID       int64  `json:"file_id"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Duration     int32  `json:"duration"`
	Width        int32  `json:"width"`
	Height       int32  `json:"height"`
	Size         int64  `json:"size"`
}

type AudioContent struct {
	FileID   int64  `json:"file_id"`
	URL      string `json:"url"`
	Duration int32  `json:"duration"`
	Size     int64  `json:"size"`
}

type LocationContent struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address"`
	Name      string  `json:"name"`
}

type SystemContent struct {
	Action         string  `json:"action"`
	Detail         string  `json:"detail"`
	RelatedUserIDs []int64 `json:"related_user_ids"`
	ActorID        int64   `json:"actor_id"`
	ActorType      string  `json:"actor_type"`
	Payload        string  `json:"payload"`
}

type BotContent struct {
	BotID          int64  `json:"bot_id"`
	BotName        string `json:"bot_name"`
	BotAvatar      string `json:"bot_avatar"`
	Text           string `json:"text"`
	IsStreaming    bool   `json:"is_streaming"`
	ThinkingTimeMs int32  `json:"thinking_time_ms"`
	RawPayload     string `json:"raw_payload"`
}

type CustomContent struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// ---- Content type to JSONContent conversion ----

func marshalFrom(from any) JSONContent {
	b, err := json.Marshal(from)
	if err != nil {
		return JSONContent{}
	}
	var jc JSONContent
	json.Unmarshal(b, &jc)
	return jc
}

func unmarshalTo(jc JSONContent, to any) error {
	b, err := json.Marshal(jc)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

func (c TextContent) ToJSONContent() JSONContent     { return marshalFrom(c) }
func (c ImageContent) ToJSONContent() JSONContent    { return marshalFrom(c) }
func (c FileContent) ToJSONContent() JSONContent     { return marshalFrom(c) }
func (c VideoContent) ToJSONContent() JSONContent    { return marshalFrom(c) }
func (c AudioContent) ToJSONContent() JSONContent    { return marshalFrom(c) }
func (c LocationContent) ToJSONContent() JSONContent { return marshalFrom(c) }
func (c SystemContent) ToJSONContent() JSONContent   { return marshalFrom(c) }
func (c BotContent) ToJSONContent() JSONContent      { return marshalFrom(c) }
func (c CustomContent) ToJSONContent() JSONContent   { return marshalFrom(c) }

func ParseTextContent(jc JSONContent) TextContent { var c TextContent; unmarshalTo(jc, &c); return c }
func ParseImageContent(jc JSONContent) ImageContent {
	var c ImageContent
	unmarshalTo(jc, &c)
	return c
}
func ParseFileContent(jc JSONContent) FileContent { var c FileContent; unmarshalTo(jc, &c); return c }
func ParseVideoContent(jc JSONContent) VideoContent {
	var c VideoContent
	unmarshalTo(jc, &c)
	return c
}
func ParseAudioContent(jc JSONContent) AudioContent {
	var c AudioContent
	unmarshalTo(jc, &c)
	return c
}
func ParseLocationContent(jc JSONContent) LocationContent {
	var c LocationContent
	unmarshalTo(jc, &c)
	return c
}
func ParseSystemContent(jc JSONContent) SystemContent {
	var c SystemContent
	unmarshalTo(jc, &c)
	return c
}
func ParseBotContent(jc JSONContent) BotContent { var c BotContent; unmarshalTo(jc, &c); return c }
func ParseCustomContent(jc JSONContent) CustomContent {
	var c CustomContent
	unmarshalTo(jc, &c)
	return c
}

func (j JSONArray) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONArray) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

const (
	MessageStatusNormal    int32 = consts.MsgStatusNormal
	MessageStatusRecalled  int32 = consts.MsgStatusRecalled
	MessageStatusEdited    int32 = consts.MsgStatusEdited
	MessageStatusStreaming int32 = consts.MsgStatusStreaming
)

const (
	MsgTypeText     int32 = consts.MsgTypeText
	MsgTypeImage    int32 = consts.MsgTypeImage
	MsgTypeFile     int32 = consts.MsgTypeFile
	MsgTypeVideo    int32 = consts.MsgTypeVideo
	MsgTypeAudio    int32 = consts.MsgTypeAudio
	MsgTypeLocation int32 = consts.MsgTypeLocation
	MsgTypeSystem   int32 = consts.MsgTypeSystem
	MsgTypeCustom   int32 = consts.MsgTypeCustom
	MsgTypeBot      int32 = consts.MsgTypeBot
)
