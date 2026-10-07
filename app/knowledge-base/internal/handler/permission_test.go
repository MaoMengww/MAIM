package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestGetCallerID_UserID(t *testing.T) {
	const callerID = "01902ee3-8b7e-7fa1-96fd-ec908c0ace21"
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("user-id", callerID))
	if got := getCallerID(ctx); got != callerID {
		t.Fatalf("expected %s, got %s", callerID, got)
	}
}

func TestGetCallerID_XUserID(t *testing.T) {
	const callerID = "01902ee3-8b7e-7fa1-96fd-ec908c0ace21"
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-user-id", callerID))
	if got := getCallerID(ctx); got != callerID {
		t.Fatalf("expected %s, got %s", callerID, got)
	}
}

func TestGetCallerID_XUserIDWins(t *testing.T) {
	const callerID = "01902ee3-8b7e-7fa1-96fd-ec908c0ace21"
	ctx := metadata.NewIncomingContext(t.Context(),
		metadata.Pairs("x-user-id", callerID, "user-id", "01902ee3-8b7e-7fa1-96fd-ec908c0ace22"))
	if got := getCallerID(ctx); got != callerID {
		t.Fatalf("expected %s (x-user-id priority), got %s", callerID, got)
	}
}

func TestGetCallerID_InvalidID(t *testing.T) {
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-user-id", "12345"))
	if got := getCallerID(ctx); got != "" {
		t.Fatalf("expected no caller id for a numeric identity, got %s", got)
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
