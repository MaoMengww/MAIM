package validator

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testUser struct {
	Name     string `validate:"required,min=2,max=50"`
	Email    string `validate:"required,email"`
	Age      int    `validate:"gte=0,lte=150"`
	Username string `validate:"username"`
	Phone    string `validate:"phone"`
	Password string `validate:"password"`
}

func TestValidateSuccess(t *testing.T) {
	u := testUser{
		Name: "Mao", Email: "mao@example.com", Age: 25,
		Username: "mao_meng", Phone: "13800138000", Password: "pass1234",
	}
	err := Validate(u)
	assert.NoError(t, err)
}

func TestValidateRequired(t *testing.T) {
	u := testUser{Email: "mao@example.com", Age: 25}
	err := Validate(u)
	require.Error(t, err)
}

func TestValidateMinLength(t *testing.T) {
	u := testUser{
		Name: "A", Email: "mao@example.com", Age: 25,
		Username: "mao_meng", Phone: "13800138000", Password: "pass1234",
	}
	err := Validate(u)
	require.Error(t, err)
}

func TestValidateEmail(t *testing.T) {
	u := testUser{
		Name: "Mao", Email: "invalid", Age: 25,
		Username: "mao_meng", Phone: "13800138000", Password: "pass1234",
	}
	err := Validate(u)
	require.Error(t, err)
}

func TestValidateAgeRange(t *testing.T) {
	u := testUser{
		Name: "Mao", Email: "mao@example.com", Age: -1,
		Username: "mao_meng", Phone: "13800138000", Password: "pass1234",
	}
	err := Validate(u)
	require.Error(t, err)

	u.Age = 200
	err = Validate(u)
	require.Error(t, err)
}

func TestVar(t *testing.T) {
	err := Var("test@example.com", "email")
	assert.NoError(t, err)

	err = Var("bad", "email")
	assert.Error(t, err)
}

func TestRegisterValidation(t *testing.T) {
	err := RegisterValidation("even", func(fl validator.FieldLevel) bool {
		return fl.Field().Int()%2 == 0
	})
	assert.NoError(t, err)
}

func TestEngine(t *testing.T) {
	v := Engine()
	assert.NotNil(t, v)
}

func TestValidateEmptyStruct(t *testing.T) {
	type empty struct{}
	err := Validate(empty{})
	assert.NoError(t, err)
}

func TestUsernameValidation(t *testing.T) {
	tests := []struct {
		name     string
		username string
		valid    bool
	}{
		{"valid", "mao_meng", true},
		{"valid_uppercase", "MAO_MENG", true},
		{"valid_numbers", "user123", true},
		{"too_short", "ab", false},
		{"too_long", strings.Repeat("a", 33), false},
		{"special_chars", "user@name", false},
		{"with_dash", "user-name", false},
		{"chinese", "用户名", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Var(tt.username, "username")
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestPhoneValidation(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		valid bool
	}{
		{"valid_138", "13800138000", true},
		{"valid_159", "15912345678", true},
		{"valid_188", "18888888888", true},
		{"too_short", "1380013800", false},
		{"too_long", "138001380001", false},
		{"bad_prefix", "12800138000", false},
		{"bad_prefix2", "10000138000", false},
		{"letters", "1380013800a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Var(tt.phone, "phone")
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestPasswordValidation(t *testing.T) {
	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{"valid", "pass1234", true},
		{"valid_complex", "Abc12345xyz", true},
		{"no_digit", "password", false},
		{"no_letter", "12345678", false},
		{"too_short", "ab1", false},
		{"too_long", strings.Repeat("a1", 33), false},
		{"exactly_8", "abcd1234", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Var(tt.password, "password")
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestTranslateError(t *testing.T) {
	u := testUser{}
	err := Validate(u)
	require.Error(t, err)
	msg := TranslateError(err)
	assert.Contains(t, msg, "Name")
	assert.Contains(t, msg, "required")
}

func TestTranslateErrorNil(t *testing.T) {
	assert.Equal(t, "", TranslateError(nil))
}

func TestTranslateErrorNonValidation(t *testing.T) {
	err := assert.AnError
	msg := TranslateError(err)
	assert.Equal(t, err.Error(), msg)
}
