package messageservicelogic

import (
	"testing"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
)

func TestModelToPbMessage_Text(t *testing.T) {
	msg := &model.Message{
		ID:        1,
		ConvID:    100,
		SenderID:  10,
		Seq:       1,
		MsgType:   model.MsgTypeText,
		Content:   model.JSONContent{"text": "hello", "mention_all": false},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Unix(1000, 0),
		UpdatedAt: time.Unix(1000, 0),
	}

	pb := modelToPbMessage(msg)
	assert.Equal(t, int64(1), pb.MessageId)
	assert.Equal(t, int64(100), pb.ConversationId)
	assert.Equal(t, message.MessageType_MESSAGE_TYPE_TEXT, pb.Type)
	assert.Equal(t, message.MessageStatus_MESSAGE_STATUS_NORMAL, pb.Status)

	textContent := pb.GetText()
	assert.NotNil(t, textContent)
	assert.Equal(t, "hello", textContent.Text)
}

func TestModelToPbMessage_Image(t *testing.T) {
	msg := &model.Message{
		ID:        2,
		ConvID:    100,
		MsgType:   model.MsgTypeImage,
		Content:   model.JSONContent{"file_id": int64(100), "url": "http://img.png", "width": int32(800), "height": int32(600)},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	imgContent := pb.GetImage()
	assert.NotNil(t, imgContent)
	assert.Equal(t, int64(100), imgContent.FileId)
	assert.Equal(t, "http://img.png", imgContent.Url)
}

func TestModelToPbMessage_File(t *testing.T) {
	msg := &model.Message{
		ID:        3,
		MsgType:   model.MsgTypeFile,
		Content:   model.JSONContent{"file_id": int64(200), "name": "doc.pdf", "size": int64(1024)},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	fileContent := pb.GetFile()
	assert.NotNil(t, fileContent)
	assert.Equal(t, int64(200), fileContent.FileId)
	assert.Equal(t, "doc.pdf", fileContent.Name)
}

func TestModelToPbMessage_Video(t *testing.T) {
	msg := &model.Message{
		ID:        4,
		MsgType:   model.MsgTypeVideo,
		Content:   model.JSONContent{"file_id": int64(300), "duration": float64(120), "width": float64(1920)},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	videoContent := pb.GetVideo()
	assert.NotNil(t, videoContent)
	assert.Equal(t, int32(120), videoContent.Duration)
}

func TestModelToPbMessage_Audio(t *testing.T) {
	msg := &model.Message{
		ID:        5,
		MsgType:   model.MsgTypeAudio,
		Content:   model.JSONContent{"file_id": int64(400), "duration": float64(60)},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	audioContent := pb.GetAudio()
	assert.NotNil(t, audioContent)
	assert.Equal(t, int32(60), audioContent.Duration)
}

func TestModelToPbMessage_Location(t *testing.T) {
	msg := &model.Message{
		ID:        6,
		MsgType:   model.MsgTypeLocation,
		Content:   model.JSONContent{"latitude": 40.7128, "longitude": -74.006},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	locContent := pb.GetLocation()
	assert.NotNil(t, locContent)
	assert.Equal(t, 40.7128, locContent.Latitude)
}

func TestModelToPbMessage_System(t *testing.T) {
	msg := &model.Message{
		ID:        7,
		MsgType:   model.MsgTypeSystem,
		Content:   model.JSONContent{"action": "invite", "detail": "user joined"},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	sysContent := pb.GetSystem()
	assert.NotNil(t, sysContent)
	assert.Equal(t, "invite", sysContent.Action)
}

func TestModelToPbMessage_Bot(t *testing.T) {
	msg := &model.Message{
		ID:        8,
		MsgType:   model.MsgTypeBot,
		Content:   model.JSONContent{"bot_id": int64(500), "text": "AI reply", "raw_payload": "{}"},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	botContent := pb.GetBot()
	assert.NotNil(t, botContent)
	assert.Equal(t, "AI reply", botContent.Text)
}

func TestModelToPbMessage_Custom(t *testing.T) {
	msg := &model.Message{
		ID:        9,
		MsgType:   model.MsgTypeCustom,
		Content:   model.JSONContent{"type": "card", "data": "{}"},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	customContent := pb.GetCustom()
	assert.NotNil(t, customContent)
	assert.Equal(t, "card", customContent.Type)
}

func TestModelToPbMessage_Nil(t *testing.T) {
	pb := modelToPbMessage(nil)
	assert.Nil(t, pb)
}

func TestModelToPbMessage_WithEditCount(t *testing.T) {
	msg := &model.Message{
		ID:        10,
		MsgType:   model.MsgTypeText,
		Content:   model.JSONContent{"text": "edited"},
		EditCount: 2,
		CreatedAt: time.Unix(1000, 0),
		UpdatedAt: time.Unix(2000, 0),
	}

	pb := modelToPbMessage(msg)
	assert.Equal(t, int32(2), pb.EditCount)
	assert.Equal(t, int64(2000), pb.EditedAt)
}

func TestModelToPbMessage_NoEditCount(t *testing.T) {
	msg := &model.Message{
		ID:        11,
		MsgType:   model.MsgTypeText,
		Content:   model.JSONContent{"text": "original"},
		EditCount: 0,
		CreatedAt: time.Unix(1000, 0),
		UpdatedAt: time.Unix(1000, 0),
	}

	pb := modelToPbMessage(msg)
	assert.Equal(t, int64(0), pb.EditedAt)
}

func TestModelToPbMessage_NilContent(t *testing.T) {
	msg := &model.Message{
		ID:      12,
		MsgType: model.MsgTypeText,
		Content: nil,
	}

	pb := modelToPbMessage(msg)
	assert.NotNil(t, pb)
	// With nil content, it should still return basic fields
	assert.Equal(t, int64(12), pb.MessageId)
}

func TestModelToPbMessage_Resuming(t *testing.T) {
	msg := &model.Message{
		ID:        13,
		MsgType:   model.MsgTypeBot,
		Content:   model.JSONContent{"text": "streaming...", "is_streaming": true},
		Status:    model.MessageStatusNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	pb := modelToPbMessage(msg)
	botContent := pb.GetBot()
	assert.NotNil(t, botContent)
	assert.True(t, botContent.IsStreaming)
}
