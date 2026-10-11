package model

import (
	"context"
	"fmt"
	"gorm.io/gorm/schema"
	"reflect"
)

// Nullable UUID storage keeps the absent nonhuman requester absent on both
// inserts and full-row saves, while the in-memory/public contract stays string.
type nullableUUIDSerializer struct{}

func (nullableUUIDSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, value any) error {
	text := ""
	switch value := value.(type) {
	case nil:
	case string:
		text = value
	case []byte:
		text = string(value)
	default:
		return fmt.Errorf("invalid UUID storage type %T", value)
	}
	return field.Set(ctx, dst, text)
}
func (nullableUUIDSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, value any) (any, error) {
	if value == "" {
		return nil, nil
	}
	return value, nil
}
func init() { schema.RegisterSerializer("nullable_uuid", nullableUUIDSerializer{}) }
