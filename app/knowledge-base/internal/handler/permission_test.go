package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestGetCallerID_UserID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-id", "67890"))
	if got := getCallerID(ctx); got != 67890 {
		t.Fatalf("expected 67890, got %d", got)
	}
}

func TestGetCallerID_XUserID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-user-id", "99999"))
	if got := getCallerID(ctx); got != 99999 {
		t.Fatalf("expected 99999, got %d", got)
	}
}

func TestGetCallerID_XUserIDWins(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-user-id", "11111", "user-id", "22222"))
	if got := getCallerID(ctx); got != 11111 {
		t.Fatalf("expected 11111 (x-user-id priority), got %d", got)
	}
}

func TestGetCallerID_InvalidID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-user-id", "not-a-number"))
	if got := getCallerID(ctx); got != 0 {
		t.Fatalf("expected 0 for invalid number, got %d", got)
	}
}

func TestGetKBIDFromContext(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("kb-id", "555"))
	if got := getKBIDFromContext(ctx); got != 555 {
		t.Fatalf("expected 555, got %d", got)
	}
}

func TestGetKBIDFromContext_Missing(t *testing.T) {
	if got := getKBIDFromContext(context.Background()); got != 0 {
		t.Fatalf("expected 0 for missing kb-id, got %d", got)
	}
}

func TestIsAdmin(t *testing.T) {
	if isAdmin(context.Background()) {
		t.Fatal("expected isAdmin to return false")
	}
}

func TestContentType_Mapping(t *testing.T) {
	cases := []struct{ fileType, expected string }{
		{"pdf", "application/pdf"},
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"md", "text/markdown"},
		{"html", "text/html"},
		{"txt", "text/plain"},
		{"unknown", "text/plain"},
	}
	for _, c := range cases {
		if got := contentType(c.fileType); got != c.expected {
			t.Errorf("contentType(%q) = %q, want %q", c.fileType, got, c.expected)
		}
	}
}

func TestParseJSONMeta(t *testing.T) {
	m := parseJSONMeta(`{"key":"val"}`)
	if m["key"] != "val" {
		t.Fatalf("expected val, got %v", m["key"])
	}
}

func TestParseJSONMeta_Empty(t *testing.T) {
	m := parseJSONMeta("")
	if m == nil {
		t.Fatal("expected non-nil map for empty input")
	}
}

func TestParseJSONMeta_Invalid(t *testing.T) {
	m := parseJSONMeta("{bad-json}")
	if m == nil {
		t.Fatal("expected non-nil map for invalid input")
	}
}
