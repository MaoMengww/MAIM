package domain

type EmbeddingRequest struct {
	Model   string
	Input   []string
	BaseURL string
	APIKey  string
}

type RerankRequest struct {
	Model           string
	Query           string
	Documents       []string
	TopN            int
	ReturnDocuments bool
	BaseURL         string
	APIKey          string
	Extra           map[string]string
}

type RerankResult struct {
	Index          int
	RelevanceScore float64
	Document       string
}

type RerankResponse struct {
	Results []RerankResult
	Model   string
	Usage   UsageInfo
}

type UsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
