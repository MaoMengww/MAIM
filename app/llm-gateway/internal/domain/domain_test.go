package domain

import "testing"

func TestEmbeddingRequest_Defaults(t *testing.T) {
	req := EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: []string{"hello", "world"},
	}
	if len(req.Input) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(req.Input))
	}
}

func TestRerankRequest_Defaults(t *testing.T) {
	req := RerankRequest{
		Model:     "bge-reranker-v2-m3",
		Query:     "test",
		Documents: []string{"doc1", "doc2", "doc3"},
		TopN:      2,
	}
	if req.TopN != 2 {
		t.Errorf("expected top_n 2, got %d", req.TopN)
	}
	if len(req.Documents) != 3 {
		t.Errorf("expected 3 documents, got %d", len(req.Documents))
	}
	if req.ReturnDocuments != false {
		t.Errorf("expected return_documents false by default")
	}
}

func TestRerankResult_Fields(t *testing.T) {
	r := RerankResult{
		Index:          0,
		RelevanceScore: 0.95,
		Document:       "original text",
	}
	if r.Index != 0 {
		t.Errorf("expected index 0, got %d", r.Index)
	}
	if r.RelevanceScore < 0 || r.RelevanceScore > 1 {
		t.Errorf("relevance score should be in [0,1], got %f", r.RelevanceScore)
	}
}

func TestUsageInfo_ZeroDefaults(t *testing.T) {
	u := UsageInfo{}
	if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 {
		t.Error("expected all zero usage info by default")
	}
}

func TestModelEntry_Fields(t *testing.T) {
	e := ModelEntry{
		ModelName:          "deepseek-v3",
		Provider:           "deepseek",
		Capability:         "chat",
		InputPricePerMTok:  0.5,
		OutputPricePerMTok: 2.0,
		Status:             "active",
	}
	if e.ModelName != "deepseek-v3" {
		t.Errorf("expected deepseek-v3, got %s", e.ModelName)
	}
	if e.Capability != "chat" {
		t.Errorf("expected chat, got %s", e.Capability)
	}
	if e.Status != "active" {
		t.Errorf("expected active, got %s", e.Status)
	}
}
