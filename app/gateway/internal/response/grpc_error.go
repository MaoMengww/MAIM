package response

import (
	"github.com/gin-gonic/gin"
	bizerrors "github.com/maomeng/aim/pkg/errors"
)

// GRPCError handles errors returned from gRPC calls and maps them to HTTP responses.
// It extracts the business error code and message from the gRPC status error
// and writes an appropriate JSON error response.
func GRPCError(c *gin.Context, err error) {
	code, msg := bizerrors.FromGRPCStatus(err)
	Error(c, bizerrors.HTTPStatusFromCode(code), code, msg)
}
