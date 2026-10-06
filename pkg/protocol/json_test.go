package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/protocol"
)

func TestSyncBoundaryUsesSafeJSONNumbers(t *testing.T) {
	var request message.SyncMessagesReq
	for _, input := range []string{`{"position":"1"}`, `{"position":-1}`, `{"position":1.5}`, `{"position":9007199254740992}`, `{"position":null}`} {
		if err := protocol.Unmarshal([]byte(input), &request); err == nil {
			t.Errorf("accepted invalid sync request %s", input)
		}
	}
	raw, err := protocol.Marshal(&message.SyncMessagesResp{NextPosition: 9007199254740991})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if string(result["next_position"]) != "9007199254740991" {
		t.Fatalf("sync position is not an exact JSON number: %s", raw)
	}
	if _, err := protocol.Marshal(&message.SyncMessagesResp{NextPosition: 9007199254740992}); err == nil {
		t.Fatal("serialized an unsafe sync position")
	}
	const id = "019b0123-4567-789a-bcde-f0123456789a"
	raw, err = protocol.Marshal(&message.Message{MessageId: id, ConversationId: id, Seq: 1, Type: message.MessageType_MESSAGE_TYPE_TEXT, CreatedAt: 9007199254740992})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if string(result["seq"]) != "1" || string(result["created_at"]) != `"9007199254740992"` {
		t.Fatalf("sequence conversion altered an unrelated int64 contract: %s", raw)
	}
	if string(result["type"]) != "1" {
		t.Fatalf("message type is not the existing numeric enum contract: %s", raw)
	}
}

func TestReferenceUpdateDistinguishesKeepSetAndClear(t *testing.T) {
	const id = "019b0123-4567-789a-bcde-f0123456789a"
	var keep, set, clear bot.UpdateBotReq
	if err := protocol.Unmarshal([]byte(`{}`), &keep); err != nil {
		t.Fatal(err)
	}
	if err := protocol.Unmarshal([]byte(`{"model_id":"`+id+`"}`), &set); err != nil {
		t.Fatal(err)
	}
	if err := protocol.Unmarshal([]byte(`{"clear_model_id":true}`), &clear); err != nil {
		t.Fatal(err)
	}
	if keep.ModelId != nil || keep.ClearModelId || set.ModelId == nil || *set.ModelId != id || clear.ModelId != nil || !clear.ClearModelId {
		t.Fatal("keep, set and clear reference states became indistinguishable")
	}
	for _, input := range []string{`{"model_id":""}`, `{"model_id":"0"}`, `{"model_id":"00000000-0000-0000-0000-000000000000"}`, `{"model_id":"` + id + `","clear_model_id":true}`} {
		if err := protocol.Unmarshal([]byte(input), &set); err == nil {
			t.Errorf("accepted invalid reference update %s", input)
		}
	}
}

func TestOwnershipCannotUseMissingOrPlatformUser(t *testing.T) {
	const id = "019b0123-4567-789a-bcde-f0123456789a"
	var request bot.CreateBotReq
	for _, input := range []string{`{"owner_type":"user"}`, `{"owner_id":"` + id + `"}`, `{"owner_type":"platform","owner_id":"` + id + `"}`} {
		if err := protocol.Unmarshal([]byte(input), &request); err == nil {
			t.Errorf("accepted invalid ownership %s", input)
		}
	}
	for _, input := range []string{`{"owner_type":"platform"}`, `{"owner_type":"user","owner_id":"` + id + `"}`} {
		if err := protocol.Unmarshal([]byte(input), &request); err != nil {
			t.Fatalf("rejected valid ownership %s: %v", input, err)
		}
	}
}
