package interceptor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/zeromicro/go-zero/core/breaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestForwardingCircuitRemainsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{"open circuit", fmt.Errorf("runtime forwarding: %w", breaker.ErrServiceUnavailable), codes.Unavailable},
		{"internal defect", errors.New("private backend details"), codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnaryErrorInterceptor()(t.Context(), nil, &grpc.UnaryServerInfo{}, func(_ context.Context, _ any) (any, error) {
				return nil, tc.err
			})
			if status.Code(err) != tc.code {
				t.Fatalf("RPC=%s; want %s", status.Code(err), tc.code)
			}
			if status.Convert(err).Message() == tc.err.Error() {
				t.Fatal("transport exposed private backend error")
			}
		})
	}
}
