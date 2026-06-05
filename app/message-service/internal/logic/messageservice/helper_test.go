package messageservicelogic

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/IBM/sarama/mocks"
	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type testSvcCtx struct {
	SvcCtx *svc.ServiceContext
	DBMock sqlmock.Sqlmock
	kMock  *mocks.SyncProducer
}

func newTestSvcCtx(t *testing.T) *testSvcCtx {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	sf, err := snowflake.NewNode(1)
	require.NoError(t, err)

	kMock := mocks.NewSyncProducer(t, nil)

	db := &database.DB{DB: gormDB}
	return &testSvcCtx{
		SvcCtx: &svc.ServiceContext{
			Config: config.Config{
				Message: config.MessageConfig{
					RecallWindowSeconds: 120,
					EditWindowSeconds:   120,
					MaxPageSize:         100,
				},
			},
			DB:                      db,
			Snowflake:               sf,
			MessageCreatedProducer:  kafka.NewTestProducer(kMock),
			MessageRecalledProducer: kafka.NewTestProducer(kMock),
			MessageEditedProducer:   kafka.NewTestProducer(kMock),
			MessageDeletedProducer:  kafka.NewTestProducer(kMock),
			MessageRepo:             repo.NewMessageRepo(db),
			InboxRepo:               repo.NewInboxRepo(db),
			BroadcastRepo:           repo.NewBroadcastRepo(db),
			SequenceRepo:            repo.NewSequenceRepo(db),
			ConvClient:              newMockConvMember(true),
			UserClient:              &mockUserClient{ids: []int64{}},
			FriendClient:            &mockFriendClient{},
		},
		DBMock: mock,
		kMock:  kMock,
	}
}

func (t *testSvcCtx) ExpectKafka() {
	t.kMock.ExpectSendMessageAndSucceed()
}
