package response

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	bizerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/core/breaker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCErrorDistinguishesOpenCircuitFromInternalFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   int
	}{
		{"open circuit", fmt.Errorf("forwarding: %w", breaker.ErrServiceUnavailable), http.StatusServiceUnavailable, bizerrors.CodeServiceDown},
		{"server defect", status.Error(codes.Internal, "internal server error"), http.StatusInternalServerError, bizerrors.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			router := gin.New()
			router.GET("/", func(c *gin.Context) { GRPCError(c, tc.err) })
			router.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
			var body Response
			if err := json.Unmarshal(writer.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if writer.Code != tc.status || body.Code != tc.code {
				t.Fatalf("HTTP=%d business=%d; want HTTP=%d business=%d", writer.Code, body.Code, tc.status, tc.code)
			}
		})
	}
}
