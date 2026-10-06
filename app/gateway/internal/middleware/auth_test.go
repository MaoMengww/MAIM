package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
