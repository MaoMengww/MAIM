//go:build integration

package messageservicelogic

import (
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	pkgerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/stretchr/testify/require"
)

// Reading by ID must report forbidden for non-members and absent only once the
// caller's own deletion overlay hides the message, so authorization is never
// masked by account-level visibility.
func TestMessageByIDAuthorizationPrecedesAccountVisibility(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	convID, outsider, body := convs[0], ids[30], ids[40]
	t.Cleanup(func() {
		require.NoError(t, s.DB.Exec("DELETE FROM messaging.personal_message_deletions WHERE user_id = ?", uid).Error)
	})
	syncAppend(t, s, uid, convID, body, "shared body")

	visible, err := NewGetMessageByIDLogic(callerContext(t.Context(), uid), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	require.NoError(t, err)
	require.Equal(t, "shared body", visible.Message.GetText().GetText())

	_, err = NewGetMessageByIDLogic(callerContext(t.Context(), outsider), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	require.ErrorIs(t, err, ErrNotMember)

	inserted, err := s.MessageRepo.InsertPersonalDeletion(t.Context(), s.DB.DB, uid, convID, body)
	require.NoError(t, err)
	require.True(t, inserted)
	_, err = NewGetMessageByIDLogic(callerContext(t.Context(), uid), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	bizErr, ok := pkgerrors.IsBizError(err)
	require.True(t, ok)
	require.Equal(t, pkgerrors.CodeNotFound, bizErr.Code)
}
