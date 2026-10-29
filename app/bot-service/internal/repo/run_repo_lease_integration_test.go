package repo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestOutboxAcknowledgementRequiresLiveLease(t *testing.T) {
	dsn := os.Getenv("BOT_TEST_DSN")
	if dsn == "" {
		t.Skip("BOT_TEST_DSN must point to an isolated PostgreSQL database")
	}
	db, err := database.NewDB(config.DatabaseConfig{DSN: dsn, MaxOpenConn: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		t.Fatal(err)
	}

	interactionID, runID := testOutboxID(t), testOutboxID(t)
	convID, botID, bindingID, requesterID := testOutboxID(t), testOutboxID(t), testOutboxID(t), testOutboxID(t)
	if err := db.Exec("INSERT INTO bot.interactions (id,bot_id,conv_id,binding_id,requester_id) VALUES (?,?,?,?,?)", interactionID, botID, convID, bindingID, requesterID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO bot.runs (id,interaction_id,bot_id,conv_id,binding_id,requester_id,controller_id,controller_member_id,input_key,input_hash,status,stage,config)
		VALUES (?,?,?,?,?,?,?,?,?,'hash','accepted','accepted','{}')`, runID, interactionID, botID, convID, bindingID, requesterID, requesterID, requesterID, runID).Error; err != nil {
	}
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM bot.run_outbox WHERE run_id = ?", runID).Error
		_ = db.Exec("DELETE FROM bot.runs WHERE id = ?", runID).Error
		_ = db.Exec("DELETE FROM bot.interactions WHERE id = ?", interactionID).Error
	})

	stale := model.RunOutbox{ID: testOutboxID(t), RunID: runID, Kind: "updated", Version: 1, Payload: []byte(`{}`), LeaseToken: testStringPointer(testOutboxID(t)), Attempts: 1}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE bot.run_outbox SET lease_until = NOW() - INTERVAL '1 second' WHERE id = ?", stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRunRepo(db)
	for name, operation := range map[string]func(context.Context, *model.RunOutbox) error{
		"complete": repo.Complete,
		"retry":    repo.Retry,
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(t.Context(), &stale); status.Code(err) != codes.Aborted {
				t.Fatalf("stale lease operation error = %v; want Aborted", err)
			}
			var current model.RunOutbox
			if err := db.First(&current, "id = ?", stale.ID).Error; err != nil {
				t.Fatal(err)
			}
			if current.CompletedAt != nil || current.LeaseToken == nil || current.LeaseUntil == nil {
				t.Fatalf("stale operation mutated lease state: %+v", current)
			}
		})
	}

	live := model.RunOutbox{ID: testOutboxID(t), RunID: runID, Kind: "updated", Version: 2, Payload: []byte(`{}`), LeaseToken: testStringPointer(testOutboxID(t)), LeaseUntil: testTimePointer(time.Now().Add(time.Minute)), Attempts: 1}
	if err := db.Create(&live).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), &live); err != nil {
		t.Fatalf("complete live lease: %v", err)
	}
	var completed model.RunOutbox
	if err := db.First(&completed, "id = ?", live.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.CompletedAt == nil || completed.LeaseToken != nil || completed.LeaseUntil != nil {
		t.Fatalf("live completion did not settle lease: %+v", completed)
	}
}

func testOutboxID(t *testing.T) string {
	t.Helper()
	id, err := identity.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testStringPointer(value string) *string { return &value }

func testTimePointer(value time.Time) *time.Time { return &value }
