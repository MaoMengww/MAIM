package server

import (
	"context"
	"github.com/maomeng/aim/app/message-service/internal/client"
	"net"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mockConvClient struct{ client.ConvClient }

func (m *mockConvClient) IsMember(_ context.Context, _, _ int64) (bool, error) { return true, nil }
func (m *mockConvClient) GetMuteStatus(_ context.Context, _, _ int64) (bool, bool, int64, error) {
	return false, false, 0, nil
}
func (m *mockConvClient) GetMembers(_ context.Context, _ int64) ([]int64, error) {
	return []int64{10, 11}, nil
}
func (m *mockConvClient) GetConvMembers(_ context.Context, _ int64) ([]int64, error) {
	return []int64{10, 11}, nil
}

func setupTestServer(t *testing.T) (message.MessageServiceClient, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	sf, err := snowflake.NewNode(1)
	require.NoError(t, err)

	db := &database.DB{DB: gormDB}
	svcCtx := &svc.ServiceContext{
		Config: config.Config{
			Message: config.MessageConfig{
				RecallWindowSeconds: 120,
				EditWindowSeconds:   120,
				MaxPageSize:         100,
			},
		},
		DB:            db,
		Snowflake:     sf,
		MessageRepo:   repo.NewMessageRepo(db),
		InboxRepo:     repo.NewInboxRepo(db),
		BroadcastRepo: repo.NewBroadcastRepo(db),
		SequenceRepo:  repo.NewSequenceRepo(db),
		ConvClient:    new(mockConvClient),
	}

	srv := NewMessageServiceServer(svcCtx)

	bufSize := 1024 * 1024
	lis := bufconn.Listen(bufSize)
	grpcSrv := grpc.NewServer()
	message.RegisterMessageServiceServer(grpcSrv, srv)

	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := message.NewMessageServiceClient(conn)

	cleanup := func() {
		conn.Close()
		grpcSrv.Stop()
	}

	return client, mock, cleanup
}

func TestServer_GetMessageByID_NotFound(t *testing.T) {
	client, mock, cleanup := setupTestServer(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	resp, err := client.GetMessageByID(context.Background(), &message.GetMessageByIDReq{
		MessageId: 999,
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServer_BatchGetMessages_Empty(t *testing.T) {
	client, _, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := client.BatchGetMessages(context.Background(), &message.BatchGetMessagesReq{
		MessageIds: []int64{},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Messages)
}

func TestServer_SearchMessages_NoCondition(t *testing.T) {
	client, _, cleanup := setupTestServer(t)
	defer cleanup()
	convID := int64(100)

	resp, err := client.SearchMessages(context.Background(), &message.SearchMessagesReq{
		UserId:         10,
		ConversationId: &convID,
		Pagination:     &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestServer_ForwardMessage_EmptyIDs(t *testing.T) {
	client, _, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := client.ForwardMessage(context.Background(), &message.ForwardMessageReq{
		MessageIds:           []int64{},
		TargetConversationId: 200,
		FromUserId:           10,
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestServer_SendBroadcast_EmptyContent(t *testing.T) {
	client, _, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := client.SendBroadcast(context.Background(), &message.SendBroadcastReq{
		SenderId: 10,
		Content:  "",
		Scope:    "all",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestServer_GetMessageByID_Success(t *testing.T) {
	client, mock, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, 1, `{"text":"hello"}`, 0, 1, `[]`, 0,
		time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	resp, err := client.GetMessageByID(ctx, &message.GetMessageByIDReq{MessageId: 1})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Message)
	assert.Equal(t, int64(1), resp.Message.MessageId)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServer_BatchGetMessages_Success(t *testing.T) {
	client, mock, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, 1, `{"text":"a"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(2, 100, 11, "", 2, 1, `{"text":"b"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(\$1,\$2\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnRows(rows)

	resp, err := client.BatchGetMessages(ctx, &message.BatchGetMessagesReq{
		MessageIds: []int64{1, 2},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServer_SearchMessages_RequiresSearchService(t *testing.T) {
	client, _, cleanup := setupTestServer(t)
	defer cleanup()
	convID := int64(100)

	resp, err := client.SearchMessages(context.Background(), &message.SearchMessagesReq{
		UserId:         10,
		Keyword:        "hello",
		ConversationId: &convID,
		Pagination:     &common.Pagination{Page: 1, PageSize: 20},
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestServer_GetAroundSeq_Success(t *testing.T) {
	client, mock, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(8, 100, 10, "", 8, 1, `{"text":"before"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(9, 100, 10, "", 10, 1, `{"text":"target"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(10, 100, 11, "", 12, 1, `{"text":"after"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE conv_id = \$1 AND seq BETWEEN \$2 AND \$3 ORDER BY seq ASC`).
		WithArgs(int64(100), int64(0), int64(20)).
		WillReturnRows(rows)

	resp, err := client.GetAroundSeq(ctx, &message.GetAroundSeqReq{
		ConversationId: 100,
		Seq:            10,
		UserId:         10,
		Limit:          20,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 3)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServer_NewMessageServiceServer(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	srv := NewMessageServiceServer(svcCtx)
	assert.NotNil(t, srv)
	assert.NotNil(t, srv.svcCtx)
}
