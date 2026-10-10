//go:build integration

package messageservicelogic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maomeng/aim/app/message-service/internal/es"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/pb/message"
	pkgconfig "github.com/maomeng/aim/pkg/config"
	pkgerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/protocol"
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

func TestRecalledMessageReadPathsDoNotExposeBody(t *testing.T) {
	s, uid, convs, ids := syncIntegrationContext(t)
	ctx := callerContext(t.Context(), uid)
	conv, id, body := convs[0], ids[30], "RECALLED_BODY_MUST_NOT_REAPPEAR"
	syncAppend(t, s, uid, conv, id, body)
	require.NoError(t, s.DB.Model(&model.Message{}).Where("id = ?", id).Updates(map[string]any{
		"sender_id": uid, "sender_type": "user", "raw_payload": body,
	}).Error)
	_, err := NewRecallMessageLogic(ctx, s).RecallMessage(&message.RecallMessageReq{MessageId: id, UserId: uid})
	require.NoError(t, err)

	one, err := NewGetMessageByIDLogic(ctx, s).GetMessageByID(&message.GetMessageByIDReq{MessageId: id})
	require.NoError(t, err)
	require.Equal(t, message.MessageStatus(model.MessageStatusRecalled), one.Message.Status)
	wire, err := protocol.Marshal(one)
	require.NoError(t, err)
	require.NotContains(t, string(wire), body)

	history, err := NewGetMessagesLogic(ctx, s).GetMessages(&message.GetMessagesReq{
		UserId: uid, ConversationId: conv, Pagination: &message.MessagePagination{Limit: 10},
	})
	require.NoError(t, err)
	require.Len(t, history.Messages, 1)
	wire, err = protocol.Marshal(history)
	require.NoError(t, err)
	require.NotContains(t, string(wire), body)

	rebuild, err := NewSyncMessagesLogic(ctx, s).SyncMessages(&message.SyncMessagesReq{UserId: uid})
	require.NoError(t, err)
	require.True(t, rebuild.RebuildRequired)
	wire, err = protocol.Marshal(rebuild)
	require.NoError(t, err)
	require.NotContains(t, string(wire), body)

	// The search index may lag recall; current visibility, not an old highlight,
	// decides disclosure. Equal content in a different message remains readable.
	resultID := ids[31]
	syncAppend(t, s, uid, conv, resultID, body)
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": map[string]any{"total": map[string]int{"value": 2}, "hits": []any{
				map[string]any{"_source": map[string]string{"message_id": id}, "highlight": map[string][]string{"text": {body}}},
				map[string]any{"_source": map[string]string{"message_id": resultID}, "highlight": map[string][]string{"text": {body}}},
			}},
		})
	}))
	t.Cleanup(index.Close)
	s.ESClient, err = es.NewClient(pkgconfig.ElasticsearchConfig{Addresses: []string{index.URL}})
	require.NoError(t, err)
	search, err := NewSearchMessagesLogic(ctx, s).SearchMessages(&message.SearchMessagesReq{
		UserId: uid, ConversationId: &conv, Keyword: body, Pagination: &common.Pagination{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.NotContains(t, search.Highlights, id)
	require.Equal(t, body, search.Highlights[resultID])
}
