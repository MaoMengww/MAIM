package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestGetCallerIDFromMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-user-id", "12345"))
	if got := getCallerID(ctx); got != 12345 {
		t.Fatalf("expected caller id 12345, got %d", got)
	}
}

func TestGetCallerIDMissingMetadata(t *testing.T) {
	if got := getCallerID(context.Background()); got != 0 {
		t.Fatalf("expected caller id 0, got %d", got)
	}
}
