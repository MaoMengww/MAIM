package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// IDStringToNumber converts numeric string values in JSON request bodies
// back to JSON numbers when the key contains "id". This allows the frontend
// to send Snowflake IDs as strings (to avoid JS precision loss) while
// the protobuf-based backend handlers expect int64.
func IDStringToNumber() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip webhook routes — external bots HMAC-sign their raw body,
		// and modifying it would break signature verification.
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/webhook") {
			c.Next()
			return
		}

		if c.Request.Body == nil {
			c.Next()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		c.Request.Body.Close()
		if err != nil || len(body) == 0 {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Next()
			return
		}

		modified := convertIDStrings(body)
		c.Request.Body = io.NopCloser(bytes.NewReader(modified))
		c.Next()
	}
}

func convertIDStrings(raw []byte) []byte {
	var data any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return raw
	}

	converted := walkAndConvert(data)
	result, err := json.Marshal(converted)
	if err != nil {
		return raw
	}
	return result
}

func walkAndConvert(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, elem := range val {
			result[k] = walkAndConvertWithKey(elem, k)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = walkAndConvert(elem)
		}
		return result
	default:
		return v
	}
}

func walkAndConvertWithKey(v any, key string) any {
	switch val := v.(type) {
	case string:
		if isIDKey(key) {
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				return n
			}
		}
		return val
	case json.Number:
		// json.Number from UseNumber() — try to parse as int64
		if isIDKey(key) {
			if n, err := strconv.ParseInt(string(val), 10, 64); err == nil {
				return n
			}
		}
		// Default: parse as float64 to preserve number semantics
		if f, err := val.Float64(); err == nil {
			return f
		}
		return string(val)
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, elem := range val {
			result[k] = walkAndConvertWithKey(elem, k)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = walkAndConvert(elem)
		}
		return result
	default:
		return v
	}
}

func isIDKey(key string) bool {
	return key == "id" || strings.HasSuffix(key, "_id") || strings.HasPrefix(key, "id_")
}
