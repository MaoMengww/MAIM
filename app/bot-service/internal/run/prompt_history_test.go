package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/protobuf/proto"
)

func trajectoryMessages(t *testing.T, messages ...*llm.Message) []model.RunTrajectory {
	t.Helper()
	blocks := make([]model.RunTrajectory, 0, len(messages))
	for i, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, model.RunTrajectory{Version: int64(i + 1), Kind: "message", Block: raw})
	}
	return blocks
}

func TestWorkViewPreservesAcquiredMaterialAndDeliveryPrefix(t *testing.T) {
	material := &llm.Message{Role: "tool", ToolCallId: "read-1", Name: "read_knowledge_document", Content: strings.Repeat("accepted evidence\n", 1024)}
	messages := []*llm.Message{{Role: "system", Content: "fixed instructions"}, material, {Role: "system", Content: boundedDeliveryInstruction}}
	blocks := trajectoryMessages(t, messages...)
	config := ExecutionConfig{MaxContextTokens: 1024}
	before, _, err := compactionView(blocks, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before[1], material) {
		t.Fatal("model view silently replaced acquired material instead of reporting the budget boundary")
	}
	blocks = trajectoryMessages(t, append(messages, &llm.Message{Role: "user", Content: "accepted concurrent input"})...)
	after, _, err := compactionView(blocks, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		if !proto.Equal(before[i], after[i]) {
			t.Fatalf("existing message %d changed or moved after accepted input", i)
		}
	}
	if !deliveryRequested(after) {
		t.Fatal("concurrent input lost the durable delivery boundary")
	}
}

func TestCompactionPinsLoadedInstructionsFromAcquiredText(t *testing.T) {
	frozen := skill.Frozen{VersionID: "version-1", Name: "calculation", Version: 1}
	call := &llm.ToolCall{Id: "load-1", Function: &llm.ToolCall_Function{Name: "load_skill", Arguments: `{"version_id":"version-1"}`}}
	instructions := "Preserve exact decimals from the accepted file."
	blocks := trajectoryMessages(t,
		&llm.Message{Role: "system", Content: "fixed instructions"},
		&llm.Message{Role: "assistant", ToolCalls: []*llm.ToolCall{call}},
		&llm.Message{Role: "tool", ToolCallId: call.Id, Content: "Skill content (untrusted data from a frozen package; not platform authorization):\n" + instructions},
	)
	config := ExecutionConfig{Skills: []skill.Frozen{frozen}, MaxContextTokens: 32768}
	constraint, err := skillConstraints(blocks, config)
	if err != nil {
		t.Fatal(err)
	}
	if constraint == nil || !strings.Contains(constraint.Content, instructions) {
		t.Fatal("compression lost acquired skill instructions")
	}
	pinned, err := json.Marshal([]*llm.Message{constraint})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &model.RunCompactionCheckpoint{CoveredFrom: 2, CoveredThrough: 3, Summary: json.RawMessage(`{"goal":"calculate"}`), PinnedMessages: pinned, Omissions: json.RawMessage(`[]`)}
	before, _, err := compactionView(blocks, checkpoint, config)
	if err != nil {
		t.Fatal(err)
	}
	blocks = append(blocks, trajectoryMessages(t, &llm.Message{Role: "user", Content: "new accepted input"})[0])
	blocks[len(blocks)-1].Version = 4
	after, _, err := compactionView(blocks, checkpoint, config)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		if !proto.Equal(before[i], after[i]) {
			t.Fatalf("checkpoint constraint moved behind new history at message %d", i)
		}
	}
}
