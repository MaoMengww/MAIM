//go:build integration

package messageservicelogic

import (
	"context"
	"strconv"
	"testing"

	"github.com/maomeng/aim/app/message-service/pb/message"
	pkgerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// Reading by ID must report forbidden for non-members and absent only once the
// caller's own deletion overlay hides the message, so authorization is never
// masked by account-level visibility.
func TestMessageByIDAuthorizationPrecedesAccountVisibility(t *testing.T) {
	s, uid, convs := syncIntegrationContext(t)
	convID, outsider, body := convs[0], uid+30, uid+40
	t.Cleanup(func() {
		require.NoError(t, s.DB.Exec("DELETE FROM messaging.personal_message_deletions WHERE user_id = ?", uid).Error)
	})
	syncAppend(t, s, uid, convID, body, "shared body")
	caller := func(id int64) context.Context {
		return metadata.NewIncomingContext(t.Context(), metadata.Pairs("user-id", strconv.FormatInt(id, 10)))
	}

	visible, err := NewGetMessageByIDLogic(caller(uid), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	require.NoError(t, err)
	require.Equal(t, "shared body", visible.Message.GetText().GetText())

	_, err = NewGetMessageByIDLogic(caller(outsider), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	require.ErrorIs(t, err, ErrNotMember)

	inserted, err := s.MessageRepo.InsertPersonalDeletion(t.Context(), s.DB.DB, uid, convID, body)
	require.NoError(t, err)
	require.True(t, inserted)
	_, err = NewGetMessageByIDLogic(caller(uid), s).GetMessageByID(&message.GetMessageByIDReq{MessageId: body})
	bizErr, ok := pkgerrors.IsBizError(err)
	require.True(t, ok)
	require.Equal(t, pkgerrors.CodeNotFound, bizErr.Code)
}
