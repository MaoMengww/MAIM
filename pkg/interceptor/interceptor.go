package interceptor

import (
	"context"
	"runtime/debug"
	"strconv"

	"github.com/maomeng/aim/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryErrorInterceptor converts BizError to gRPC status errors and recovers panics.
// Non-BizError errors are converted to codes.Internal without leaking details.
func UnaryErrorInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			return nil, toGRPCError(err)
		}
		return resp, nil
	}
}

// StreamErrorInterceptor is the streaming variant of UnaryErrorInterceptor.
func StreamErrorInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, stream)
		if err != nil {
			return toGRPCError(err)
		}
		return nil
	}
}

// PanicRecoveryInterceptor recovers from panics and returns a gRPC Internal error.
func PanicRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = status.Errorf(codes.Internal, "internal server error")
			}
			_ = debug.Stack() // available for logging if needed
		}()
		return handler(ctx, req)
	}
}

// UnaryClientInterceptor converts gRPC status errors on the client side.
// Used by the Gateway to ensure consistent error handling.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			return toGRPCError(err)
		}
		return nil
	}
}

// UnaryRequestIDClientInterceptor propagates request_id from context to outgoing gRPC metadata.
func UnaryRequestIDClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if requestID, ok := ctx.Value(ContextKeyRequestID).(string); ok && requestID != "" {
			ctx = grpcMetadata.AppendToOutgoingContext(ctx, "request-id", requestID)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamRequestIDClientInterceptor is the streaming variant of UnaryRequestIDClientInterceptor.
func StreamRequestIDClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if requestID, ok := ctx.Value(ContextKeyRequestID).(string); ok && requestID != "" {
			ctx = grpcMetadata.AppendToOutgoingContext(ctx, "request-id", requestID)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// ContextKeyUserID is used to store the authenticated user ID in context.
const ContextKeyUserID = "user_id"

// ContextKeyRequestID is used to store the request ID in context.
const ContextKeyRequestID = "request_id"

// UnaryUserIDInterceptor extracts user-id from gRPC metadata and injects it into context.
func UnaryUserIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = WithUserIDFromMetadata(ctx)
		return handler(ctx, req)
	}
}

// WithUserIDFromMetadata extracts user-id from gRPC incoming metadata and returns
// a new context with the value set.
func WithUserIDFromMetadata(ctx context.Context) context.Context {
	if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("user-id"); len(vals) > 0 {
			if id, err := strconv.ParseInt(vals[0], 10, 64); err == nil {
				return context.WithValue(ctx, ContextKeyUserID, id)
			}
		}
	}
	return ctx
}

// UnaryRequestIDInterceptor extracts request-id from gRPC metadata and injects it into context.
func UnaryRequestIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = WithRequestIDFromMetadata(ctx)
		return handler(ctx, req)
	}
}

// WithRequestIDFromMetadata extracts request-id from gRPC incoming metadata and returns
// a new context with the value set.
func WithRequestIDFromMetadata(ctx context.Context) context.Context {
	if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("request-id"); len(vals) > 0 {
			ctx = context.WithValue(ctx, ContextKeyRequestID, vals[0])
		}
	}
	return ctx
}

func toGRPCError(err error) error {
	if err == nil {
		return nil
	}
	// If already a gRPC status error, preserve it.
	if _, ok := status.FromError(err); ok {
		return err
	}
	return errors.ToGRPCError(err)
}
