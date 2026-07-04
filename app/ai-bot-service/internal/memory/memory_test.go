package memory

import (
	"context"
	"testing"
	"time"
)

// ── mockStore implements Store for unit tests. ──

type mockStore struct {
	profileText  string
	profileAt    time.Time
	newFactCount int
}

func (m *mockStore) SaveEpisode(_ context.Context, _ *Episode) error       { return nil }
func (m *mockStore) AddFacts(_ context.Context, _ []Fact) error             { return nil }
func (m *mockStore) Search(_ context.Context, _ Scope, _ string, _ int) ([]Memory, error) {
	return nil, nil
}
func (m *mockStore) SearchByIDs(_ context.Context, _ Scope, _ []int64, _ bool) ([]Memory, error) {
	return nil, nil
}
func (m *mockStore) SearchWithTraversal(_ context.Context, _ Scope, _ string, _ int, _ int) ([]Memory, error) {
	return nil, nil
}
func (m *mockStore) GetProfileData(_ context.Context, _, _ int64) (string, time.Time, error) {
	return m.profileText, m.profileAt, nil
}
func (m *mockStore) CountNewFactsSince(_ context.Context, _, _ int64, _ time.Time) (int, error) {
	return m.newFactCount, nil
}
func (m *mockStore) GetIncrementalFacts(_ context.Context, _, _ int64, _ time.Time) ([]ProfileFact, []ProfileFact, error) {
	return nil, nil, nil
}
func (m *mockStore) GetInitialProfileFacts(_ context.Context, _, _ int64, _ int) ([]ProfileFact, error) {
	return nil, nil
}
func (m *mockStore) UpdateProfile(_ context.Context, _, _ int64, _ string, _ time.Time) error {
	return nil
}

func newTestManager(store *mockStore) *Manager {
	return &Manager{
		store:      store,
		vectorTopK: 4,
	}
}

// ── shouldRefresh ──

func TestShouldRefresh_NoProfile(t *testing.T) {
	store := &mockStore{profileText: ""}
	m := newTestManager(store)
	if !m.shouldRefresh("", time.Time{}, 1, 1) {
		t.Error("empty profile should trigger refresh")
	}
}

func TestShouldRefresh_Cooling(t *testing.T) {
	store := &mockStore{profileText: "exists"}
	m := newTestManager(store)
	if m.shouldRefresh("exists", time.Now(), 1, 1) {
		t.Error("should not refresh within cooling period")
	}
}

func TestShouldRefresh_EnoughFacts(t *testing.T) {
	store := &mockStore{profileText: "exists", profileAt: time.Now().Add(-10 * time.Minute), newFactCount: 5}
	m := newTestManager(store)
	if !m.shouldRefresh("exists", store.profileAt, 1, 1) {
		t.Error(">=3 facts after cooling should trigger refresh")
	}
}

func TestShouldRefresh_NotEnoughFacts(t *testing.T) {
	store := &mockStore{profileText: "exists", profileAt: time.Now().Add(-10 * time.Minute), newFactCount: 2}
	m := newTestManager(store)
	if m.shouldRefresh("exists", store.profileAt, 1, 1) {
		t.Error("<3 facts should not trigger refresh")
	}
}

// ── rerank ──

func TestRerank(t *testing.T) {
	items := []Memory{
		{ID: 1, Importance: 0.5, Confidence: 0.5},
		{ID: 2, Importance: 0.9, Confidence: 0.9},
		{ID: 3, Importance: 0.3, Confidence: 0.3},
	}
	scoreMap := map[int64]float64{1: 0.2, 2: 0.8, 3: 0.2}

	rerank(items, scoreMap)

	// ID 2 should be first (highest vector score).
	if items[0].ID != 2 {
		t.Errorf("after rerank: first = %d, want 2", items[0].ID)
	}
	// ID 1 and 3 have same vector score; ID 1 should come before ID 3 by quality.
	if items[1].ID != 1 {
		t.Errorf("after rerank: second = %d, want 1", items[1].ID)
	}
	if items[2].ID != 3 {
		t.Errorf("after rerank: third = %d, want 3", items[2].ID)
	}
}

func TestRerank_Empty(t *testing.T) {
	rerank(nil, nil)
	rerank([]Memory{}, map[int64]float64{})
}

// ── prompt builders ──

func TestBuildInitialProfilePrompt(t *testing.T) {
	facts := []ProfileFact{
		{Content: "用户住在上海", Category: "location"},
		{Content: "用户主要使用 Go", Category: "skill"},
	}
	prompt := buildInitialProfilePrompt(facts)
	for _, want := range []string{"用户住在上海", "用户主要使用 Go", "stable", "SAME language", "[location]", "[skill]"} {
		if !contains(prompt, want) {
			t.Errorf("initial prompt missing %q", want)
		}
	}
}

func TestBuildInitialProfilePrompt_Empty(t *testing.T) {
	prompt := buildInitialProfilePrompt(nil)
	if prompt == "" {
		t.Error("prompt should not be empty even with no facts")
	}
}

func TestBuildIncrementalProfilePrompt(t *testing.T) {
	text := "你住在上海，使用Go。"
	newFacts := []ProfileFact{{Content: "用户刚搬到东京", Category: "location"}}
	expiredFacts := []ProfileFact{{Content: "用户住在上海", Category: "location"}}

	prompt := buildIncrementalProfilePrompt(text, newFacts, expiredFacts)

	for _, want := range []string{"你住在上海，使用Go。", "用户刚搬到东京", "用户住在上海", "expired", "SAME language", "[location]"} {
		if !contains(prompt, want) {
			t.Errorf("incremental prompt missing %q", want)
		}
	}
}

func TestBuildIncrementalProfilePrompt_NoExpired(t *testing.T) {
	prompt := buildIncrementalProfilePrompt("旧画像", []ProfileFact{{Content: "新增事实"}}, nil)
	if contains(prompt, "Facts that have expired") {
		t.Error("prompt should not include expired-facts section when none exist")
	}
	if !contains(prompt, "新增事实") {
		t.Error("prompt should include new facts")
	}
}

// ── formatMemoryContent ──

func TestFormatMemoryContent_Current(t *testing.T) {
	item := Memory{Content: "用户住在上海", TemporalHint: "current"}
	got := formatMemoryContent(item, false)
	if got != "用户住在上海" {
		t.Errorf("current non-historical query: got %q", got)
	}
}

func TestFormatMemoryContent_HistoricalExpired(t *testing.T) {
	exp := time.Now()
	item := Memory{Content: "用户住在北京", ExpiredAt: &exp}
	got := formatMemoryContent(item, true)
	want := "过去：用户住在北京"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatMemoryContent_HistoricalInvalid(t *testing.T) {
	inv := time.Now()
	item := Memory{Content: "用户住在北京", InvalidAt: &inv}
	got := formatMemoryContent(item, true)
	want := "过去：用户住在北京"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatMemoryContent_HistoricalPastHint(t *testing.T) {
	item := Memory{Content: "用户学过Java", TemporalHint: "past"}
	got := formatMemoryContent(item, true)
	want := "过去：用户学过Java"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatMemoryContent_HistoricalCurrent(t *testing.T) {
	item := Memory{Content: "用户住在上海", TemporalHint: "current"}
	got := formatMemoryContent(item, true)
	want := "当前：用户住在上海"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── parseExtractionResult ──

func TestParseExtractionResult_Valid(t *testing.T) {
	raw := `{"facts":[{"subject":"user","predicate":"main_language","object":"Go","entity_type":"language","category":"skill","content":"用户现在主要使用 Go","evidence":"我现在主要写 Go","importance":0.7,"confidence":0.9,"temporal_hint":"current"}]}`
	result, err := ParseExtractionResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result.Facts))
	}
	f := result.Facts[0]
	if f.Predicate != "main_language" || f.Object != "Go" || f.TemporalHint != "current" {
		t.Errorf("fact fields mismatch: %+v", f)
	}
}

func TestParseExtractionResult_EmptyArray(t *testing.T) {
	raw := `{"facts":[]}`
	result, err := ParseExtractionResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(result.Facts))
	}
}

func TestParseExtractionResult_InvalidJSON(t *testing.T) {
	_, err := ParseExtractionResult(`{bad json}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseExtractionResult_CodeFence(t *testing.T) {
	raw := "```json\n{\"facts\":[{\"subject\":\"user\",\"predicate\":\"likes\",\"object\":\"coffee\",\"entity_type\":\"preference\",\"category\":\"preference\",\"content\":\"likes coffee\",\"evidence\":\"I like coffee\",\"importance\":0.5,\"confidence\":0.8,\"temporal_hint\":\"unknown\"}]}\n```"
	result, err := ParseExtractionResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Facts) != 1 {
		t.Errorf("expected 1 fact, got %d", len(result.Facts))
	}
}
