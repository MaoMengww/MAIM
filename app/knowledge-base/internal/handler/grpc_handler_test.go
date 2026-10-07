package handler

import (
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestGetCallerIDFromMetadata(t *testing.T) {
	const callerID = "01902ee3-8b7e-7fa1-96fd-ec908c0ace21"
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-user-id", callerID))
	if got := getCallerID(ctx); got != callerID {
		t.Fatalf("expected caller id %s, got %s", callerID, got)
	}
}

func TestGetCallerIDMissingMetadata(t *testing.T) {
	if got := getCallerID(t.Context()); got != "" {
		t.Fatalf("expected no caller id, got %s", got)
	}
}
