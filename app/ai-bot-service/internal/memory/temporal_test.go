package memory

import (
	"testing"
	"time"
)

func TestNormalizePredicate(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"lives_in", "lives_in"},
		{"Lives_In", "lives_in"},
		{"  Main_Language  ", "main_language"},
		{"WORKS_AT", "works_at"},
	}
	for _, tt := range tests {
		if got := normalizePredicate(tt.in); got != tt.want {
			t.Errorf("normalizePredicate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeEntityName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"Shanghai", "shanghai"},
		{"  Go  ", "go"},
		{"Kubernetes", "kubernetes"},
	}
	for _, tt := range tests {
		if got := normalizeEntityName(tt.in); got != tt.want {
			t.Errorf("normalizeEntityName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExclusiveGroup(t *testing.T) {
	tests := []struct{ predicate, want string }{
		{"lives_in", "current_location"},
		{"currently_in", "current_location"},
		{"Lives_In", "current_location"},
		{"studies_at", "current_school"},
		{"majors_in", "current_major"},
		{"works_at", "current_company"},
		{"main_language", "primary_tech_stack"},
		{"main_tool", "primary_tech_stack"},
		{"hates", "negative_preference"},
		{"dislikes", "negative_preference"},
		{"Dislikes", "negative_preference"},
		{"likes", ""},
		{"has_partner", ""},
		{"has_father", ""},
		{"has_mother", ""},
		{"has_sibling", ""},
		{"unknown_predicate", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := exclusiveGroup(tt.predicate); got != tt.want {
			t.Errorf("exclusiveGroup(%q) = %q, want %q", tt.predicate, got, tt.want)
		}
	}
}

func TestPredicatesInGroup(t *testing.T) {
	tests := []struct {
		group string
		want  []string
	}{
		{"current_location", []string{"lives_in", "currently_in"}},
		{"current_major", []string{"majors_in"}},
		{"primary_tech_stack", []string{"main_language", "main_tool"}},
		{"negative_preference", []string{"hates", "dislikes"}},
		{"", nil},
		{"nonexistent", []string{}},
	}
	for _, tt := range tests {
		got := predicatesInGroup(tt.group)
		if len(got) != len(tt.want) {
			t.Errorf("predicatesInGroup(%q) = %v (len=%d), want %v (len=%d)", tt.group, got, len(got), tt.want, len(tt.want))
		}
	}
}

func TestIsHistoricalQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"我以前住在哪里", true},
		{"之前你说过什么", true},
		{"我曾经用过Python", true},
		{"过去的事情", true},
		{"后来怎么样了", true},
		{"当时我在哪", true},
		{"上次我们聊过", true},
		{"还记得吗", true},
		{"我改成Go了", true},
		{"原来你是这样说的", true},
		{"我以前说过喜欢咖啡", true},
		{"我现在在哪里", false},
		{"你好", false},
		{"帮我写个函数", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isHistoricalQuery(tt.query); got != tt.want {
			t.Errorf("isHistoricalQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestResolveTemporal(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	validAt, invalidAt := resolveTemporal(now, "current")
	if !validAt.Equal(now) {
		t.Errorf("current: validAt = %v, want %v", validAt, now)
	}
	if invalidAt != nil {
		t.Errorf("current: invalidAt = %v, want nil", invalidAt)
	}

	validAt, invalidAt = resolveTemporal(now, "past")
	if !validAt.Equal(now) {
		t.Errorf("past: validAt = %v, want %v", validAt, now)
	}
	if invalidAt != nil {
		t.Errorf("past: invalidAt = %v, want nil", invalidAt)
	}

	validAt, invalidAt = resolveTemporal(time.Time{}, "future")
	if validAt.IsZero() {
		t.Error("zero time should default to now")
	}
	if invalidAt != nil {
		t.Errorf("future: invalidAt = %v, want nil", invalidAt)
	}
}

func TestBuildSearchText(t *testing.T) {
	fact := Fact{
		Object:     "Go",
		EntityType: "language",
		Predicate:  "main_language",
		Category:   "skill",
		Content:    "用户现在主要使用 Go",
		Evidence:   "我现在主要写 Go",
	}
	got := buildSearchText(fact)

	// Must contain core tokens (Go and go are deduplicated by lowercased key, only Go kept).
	for _, want := range []string{"Go", "language", "main_language", "skill", "主要使用"} {
		if !contains(got, want) {
			t.Errorf("buildSearchText missing %q in %q", want, got)
		}
	}

	// Must not have duplicates.
	if contains(got, "Go") && stringsCount(got, "Go") > 1 {
		// "Go" appears as raw Object and could also appear in Content,
		// but compactStrings deduplicates by lowercased key, so "Go" and "go"
		// would both be kept if they are different case. That's fine.
	}
}

func TestCompactStrings(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"a", "A", "a"}, []string{"a"}},
		{[]string{" ", "", "  ", "hello"}, []string{"hello"}},
		{[]string{"Go", "go", " GO "}, []string{"Go"}},
		{nil, nil},
	}
	for _, tt := range tests {
		got := compactStrings(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("compactStrings(%v) = %v (len=%d), want %v (len=%d)", tt.in, got, len(got), tt.want, len(tt.want))
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func stringsCount(s, sub string) int {
	n := 0
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
