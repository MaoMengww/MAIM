package repo

import (
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/snowflake"
)

func TestKBRepo_WithSnowStoresNode(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("create snowflake node failed: %v", err)
	}
	r := (&KBRepo{}).WithSnow(node)
	if r.snowID == nil {
		t.Fatalf("expected snowflake node to be stored")
	}
}

func TestDocumentRepo_WithSnowStoresNode(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("create snowflake node failed: %v", err)
	}
	r := (&DocumentRepo{}).WithSnow(node)
	if r.snowID == nil {
		t.Fatalf("expected snowflake node to be stored")
	}
}

func TestDocumentStructZeroIDRequiresAppAssignment(t *testing.T) {
	doc := domain.Document{}
	if doc.ID != 0 {
		t.Fatalf("expected zero-value document id to be 0")
	}
}

func TestKnowledgeBindingStructZeroIDRequiresAppAssignment(t *testing.T) {
	binding := domain.KnowledgeBinding{}
	if binding.ID != 0 {
		t.Fatalf("expected zero-value binding id to be 0")
	}
}
