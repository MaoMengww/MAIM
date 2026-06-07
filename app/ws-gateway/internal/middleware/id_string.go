package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func IDStringToNumber() gin.HandlerFunc {
	return func(c *gin.Context) {
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
	result, _ := json.Marshal(converted)
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
		if isIDKey(key) {
			if n, err := strconv.ParseInt(string(val), 10, 64); err == nil {
				return n
			}
		}
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
