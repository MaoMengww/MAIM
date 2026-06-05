package validator

import (
	"regexp"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
)

var (
	validate          *validator.Validate
	usernameRegex     = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	chinesePhoneRegex = regexp.MustCompile(`^1[3-9]\d{9}$`)
)

func init() {
	validate = validator.New()
	registerCustomValidations()
}

func registerCustomValidations() {
	_ = validate.RegisterValidation("username", validateUsername)
	_ = validate.RegisterValidation("phone", validatePhone)
	_ = validate.RegisterValidation("password", validatePassword)
}

func validateUsername(fl validator.FieldLevel) bool {
	return usernameRegex.MatchString(fl.Field().String())
}

func validatePhone(fl validator.FieldLevel) bool {
	return chinesePhoneRegex.MatchString(fl.Field().String())
}

func validatePassword(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if utf8.RuneCountInString(s) < 8 || len(s) > 64 {
		return false
	}
	hasLetter := false
	hasDigit := false
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

func Validate(s any) error {
	return validate.Struct(s)
}

func Var(v any, tag string) error {
	return validate.Var(v, tag)
}

func RegisterValidation(tag string, fn validator.Func) error {
	return validate.RegisterValidation(tag, fn)
}

func Engine() *validator.Validate {
	return validate
}

func TranslateError(err error) string {
	if err == nil {
		return ""
	}
	ves, ok := err.(validator.ValidationErrors)
	if !ok {
		return err.Error()
	}
	msgs := make([]string, 0, len(ves))
	for _, ve := range ves {
		msgs = append(msgs, ve.Field()+" validation failed on '"+ve.Tag()+"'")
	}
	if len(msgs) == 0 {
		return err.Error()
	}
	result := ""
	for i, m := range msgs {
		if i > 0 {
			result += "; "
		}
		result += m
	}
	return result
}
