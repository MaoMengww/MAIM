package errors

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	err := New(1001, "invalid parameter")
	assert.Equal(t, 1001, err.Code)
	assert.Equal(t, "invalid parameter", err.Message)
	assert.Nil(t, err.Cause)
	assert.Contains(t, err.Error(), "code: 1001")
	assert.Contains(t, err.Error(), "msg: invalid parameter")
}

func TestWrap(t *testing.T) {
	cause := fmt.Errorf("original error")
	err := Wrap(1002, "unauthorized", cause)
	assert.Equal(t, 1002, err.Code)
	assert.Equal(t, "unauthorized", err.Message)
	assert.Equal(t, cause, err.Cause)
	assert.Contains(t, err.Error(), "cause: original error")
}

func TestWrapNilCause(t *testing.T) {
	err := Wrap(1003, "forbidden", nil)
	assert.Equal(t, 1003, err.Code)
	assert.Nil(t, err.Cause)
	assert.NotContains(t, err.Error(), "cause:")
}

func TestUnwrap(t *testing.T) {
	cause := fmt.Errorf("db connection refused")
	err := Wrap(1010, "database error", cause)
	unwrapped := stderrors.Unwrap(err)
	assert.Equal(t, cause, unwrapped)
}

func TestErrorsIs(t *testing.T) {
	cause := fmt.Errorf("connection timeout")
	err := Wrap(1007, "request timeout", cause)
	assert.True(t, stderrors.Is(err, cause))
}

func TestErrorsAs(t *testing.T) {
	err := New(1004, "not found")
	var bizErr *BizError
	assert.True(t, stderrors.As(err, &bizErr))
	assert.Equal(t, 1004, bizErr.Code)
}

func TestIsBizError(t *testing.T) {
	t.Run("biz error", func(t *testing.T) {
		err := New(1005, "conflict")
		bizErr, ok := IsBizError(err)
		assert.True(t, ok)
		assert.Equal(t, 1005, bizErr.Code)
	})

	t.Run("wrapped biz error", func(t *testing.T) {
		bizErr := New(1006, "internal error")
		wrapped := fmt.Errorf("action failed: %w", bizErr)
		result, ok := IsBizError(wrapped)
		assert.True(t, ok)
		require.NotNil(t, result)
		assert.Equal(t, 1006, result.Code)
	})

	t.Run("nil error", func(t *testing.T) {
		bizErr, ok := IsBizError(nil)
		assert.False(t, ok)
		assert.Nil(t, bizErr)
	})

	t.Run("plain error", func(t *testing.T) {
		plain := fmt.Errorf("plain error")
		bizErr, ok := IsBizError(plain)
		assert.False(t, ok)
		assert.Nil(t, bizErr)
	})
}

func TestErrorStringWithoutCause(t *testing.T) {
	err := New(2001, "custom error")
	assert.Equal(t, "code: 2001, msg: custom error", err.Error())
}

func TestPredefinedErrors(t *testing.T) {
	assert.Equal(t, 1000, ErrUnknown.Code)
	assert.Equal(t, 1001, ErrInvalidParam.Code)
	assert.Equal(t, 1002, ErrUnauthorized.Code)
	assert.Equal(t, 1003, ErrForbidden.Code)
	assert.Equal(t, 1004, ErrNotFound.Code)
	assert.Equal(t, 1005, ErrConflict.Code)
	assert.Equal(t, 1006, ErrInternal.Code)
	assert.Equal(t, 1007, ErrTimeout.Code)
	assert.Equal(t, 1008, ErrTooManyRequests.Code)
	assert.Equal(t, 1009, ErrServiceDown.Code)
	assert.Equal(t, 1010, ErrDBError.Code)
	assert.Equal(t, 1011, ErrCacheError.Code)
	assert.Equal(t, 1012, ErrMQError.Code)
	assert.Equal(t, 1013, ErrRPCError.Code)
	assert.Equal(t, 1014, ErrIOError.Code)
}

func TestErrorsAsMultipleWraps(t *testing.T) {
	cause := New(1004, "not found")
	wrapped1 := fmt.Errorf("handler: %w", cause)
	wrapped2 := fmt.Errorf("controller: %w", wrapped1)
	var bizErr *BizError
	assert.True(t, stderrors.As(wrapped2, &bizErr))
	assert.Equal(t, 1004, bizErr.Code)
}
