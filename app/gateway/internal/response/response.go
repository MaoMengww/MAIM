package response

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	bizerrors "github.com/maomeng/aim/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// HTTP status codes
const (
	StatusOK             = 200
	StatusCreated        = 201
	StatusNoContent      = 204
	StatusBadRequest     = 400
	StatusUnauthorized   = 401
	StatusForbidden      = 403
	StatusNotFound       = 404
	StatusConflict       = 409
	StatusRequestTimeout = 408
	StatusInternal       = 500
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type PageData struct {
	List       any   `json:"list"`
	Total      int64 `json:"total"`
	Page       int32 `json:"page,omitempty"`
	PageSize   int32 `json:"page_size,omitempty"`
	TotalPages int32 `json:"total_pages,omitempty"`
}

var protoJSON = protojson.MarshalOptions{
	UseProtoNames:  true,
	UseEnumNumbers: true,
}

// prepareData converts proto messages to plain Go types using protojson,
// converting int64 ID fields to JSON strings so frontend JS doesn't lose precision.
func prepareData(data any) any {
	if msg, ok := data.(proto.Message); ok {
		raw, err := protoJSON.Marshal(msg)
		if err == nil {
			var v any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if dec.Decode(&v) == nil {
				return convertIDsToStrings(v)
			}
		}
	}
	return data
}

// convertIDsToStrings recursively walks the response and converts ID numbers to strings.
func convertIDsToStrings(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, elem := range val {
			if isIDKey(k) {
				result[k] = idNumberToString(elem)
			} else {
				result[k] = convertIDsToStrings(elem)
			}
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = convertIDsToStrings(elem)
		}
		return result
	default:
		return v
	}
}

func idNumberToString(v any) any {
	switch val := v.(type) {
	case json.Number:
		return string(val)
	}
	return v
}

func isIDKey(key string) bool {
	return key == "id" || strings.HasSuffix(key, "_id") || strings.HasPrefix(key, "id_")
}

func Success(c *gin.Context, data any) {
	c.JSON(StatusOK, Response{Code: 0, Message: "ok", Data: prepareData(data)})
}

func Created(c *gin.Context, data any) {
	c.JSON(StatusCreated, Response{Code: 0, Message: "created", Data: prepareData(data)})
}

func NoContent(c *gin.Context) {
	c.JSON(StatusNoContent, nil)
}

func Error(c *gin.Context, httpStatus int, code int, message string) {
	c.JSON(httpStatus, Response{Code: code, Message: message})
}

func BadRequest(c *gin.Context, message string) {
	Error(c, StatusBadRequest, bizerrors.CodeInvalidParam, message)
}

func Unauthorized(c *gin.Context, message string) {
	Error(c, StatusUnauthorized, bizerrors.CodeUnauthorized, message)
}

func Forbidden(c *gin.Context, message string) {
	Error(c, StatusForbidden, bizerrors.CodeForbidden, message)
}

func NotFound(c *gin.Context, message string) {
	Error(c, StatusNotFound, bizerrors.CodeNotFound, message)
}

func Conflict(c *gin.Context, message string) {
	Error(c, StatusConflict, bizerrors.CodeConflict, message)
}

func NotImplemented(c *gin.Context, args ...string) {
	msg := "not implemented"
	if len(args) > 0 {
		msg = args[0]
	}
	Error(c, StatusInternal, bizerrors.CodeInternal, msg)
}

func InternalError(c *gin.Context, message string) {
	Error(c, StatusInternal, bizerrors.CodeInternal, message)
}

func PageDataResult(list any, total int64, page, pageSize int32) *PageData {
	if pageSize <= 0 {
		pageSize = 20
	}
	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) != 0 {
		totalPages++
	}
	return &PageData{List: list, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages}
}
