package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

type testContextKey string

func newTestGinContext(reqCtx context.Context) *gin.Context {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req.WithContext(reqCtx)
	return c
}

func TestWithGRPCMetadataUsesRequestContext(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.WithValue(context.Background(), testContextKey("trace"), "kept"))
	c := newTestGinContext(baseCtx)

	ctx := WithGRPCMetadata(c)

	if got := ctx.Value(testContextKey("trace")); got != "kept" {
		t.Fatalf("expected request context value to be preserved, got %v", got)
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-context.Background().Done():
		t.Fatal("unreachable")
	default:
		t.Fatal("expected returned context to be canceled when request context is canceled")
	}
}

func TestWithGRPCMetadataAppendsOutgoingMetadata(t *testing.T) {
	c := newTestGinContext(context.Background())
	c.Set(CtxKeyUserID, int64(123))
	c.Set(CtxKeyDeviceID, "device-1")
	c.Set(CtxKeyRequestID, "request-1")

	ctx := WithGRPCMetadata(c)
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}

	assertMetadataValue(t, md, "user-id", "123")
	assertMetadataValue(t, md, "device-id", "device-1")
	assertMetadataValue(t, md, "request-id", "request-1")
}

func assertMetadataValue(t *testing.T, md metadata.MD, key, want string) {
	t.Helper()

	vals := md.Get(key)
	if len(vals) != 1 || vals[0] != want {
		t.Fatalf("metadata %q = %v, want [%q]", key, vals, want)
	}
}
