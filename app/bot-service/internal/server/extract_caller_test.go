package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestExtractChatCaller_HappyPath(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-user-id", "42", "x-username", "alice", "x-user-language", "zh-CN"))
	uid, name, lang := extractChatCaller(ctx)
	if uid != 42 {
		t.Fatalf("expected uid 42, got %d", uid)
	}
	if name != "alice" {
		t.Fatalf("expected username alice, got %s", name)
	}
	if lang != "zh-CN" {
		t.Fatalf("expected language zh-CN, got %s", lang)
	}
}

func TestExtractChatCaller_MissingMetadata(t *testing.T) {
	uid, name, lang := extractChatCaller(context.Background())
	if uid != 0 {
		t.Fatalf("expected uid 0, got %d", uid)
	}
	if name != "" {
		t.Fatalf("expected empty name, got %s", name)
	}
	if lang != "" {
		t.Fatalf("expected empty language, got %s", lang)
	}
}

func TestExtractChatCaller_PartialMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-user-id", "99"))
	uid, name, lang := extractChatCaller(ctx)
	if uid != 99 {
		t.Fatalf("expected uid 99, got %d", uid)
	}
	if name != "" {
		t.Fatalf("expected empty name, got %s", name)
	}
	if lang != "" {
		t.Fatalf("expected empty language, got %s", lang)
	}
}
