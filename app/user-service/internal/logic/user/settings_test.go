package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

func TestGetSettings_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, "019b0123-4567-789a-bcde-0000000003e7").Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.GetSettings(testUserContext(t, "019b0123-4567-789a-bcde-0000000003e7"), "019b0123-4567-789a-bcde-0000000003e7")
	assert.Error(t, err)
}
