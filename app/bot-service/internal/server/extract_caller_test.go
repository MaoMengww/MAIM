package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestExtractChatCaller_HappyPath(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("user-id", "01960000-0000-7000-8000-000000000001", "x-username", "alice", "x-user-language", "zh-CN"))
	uid, name, lang := extractChatCaller(ctx)
	if uid != "01960000-0000-7000-8000-000000000001" {
		t.Fatalf("unexpected uid %s", uid)
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
	if uid != "" {
		t.Fatalf("expected no user identity, got %s", uid)
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
		metadata.Pairs("user-id", "01960000-0000-7000-8000-000000000002"))
	uid, name, lang := extractChatCaller(ctx)
	if uid != "01960000-0000-7000-8000-000000000002" {
		t.Fatalf("unexpected uid %s", uid)
	}
	if name != "" {
		t.Fatalf("expected empty name, got %s", name)
	}
	if lang != "" {
		t.Fatalf("expected empty language, got %s", lang)
	}
}
