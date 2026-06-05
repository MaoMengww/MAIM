package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	eventpkg "github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
)

type DocumentUploadedHandler struct {
	DocRepo            domain.DocumentRepo
	KBRepo             domain.KBRepo
	IngestPipe         *pipeline.IngestPipeline
	WikiIngestPipe     *pipeline.WikiIngestPipeline
	Logger             logx.Logger
	DefaultEmbeddingID int64
}

type DocumentUploadedEvent struct {
	DocID int64 `json:"doc_id"`
}

func (h *DocumentUploadedHandler) Handle(ctx context.Context, key, value []byte) error {
	var event DocumentUploadedEvent
	logger := h.Logger.WithContext(ctx)
	if err := json.Unmarshal(value, &event); err != nil {
		logger.Errorf("unmarshal document.uploaded event failed: %v", err)
		return err
	}

	logger.Infof("document event received: event=upload doc_id=%d", event.DocID)

	go func() {
		pipeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		logger := h.Logger.WithContext(pipeCtx)

		doc, err := h.DocRepo.Get(pipeCtx, event.DocID)
		if err != nil {
			logger.Errorf("get document %d failed: %v", event.DocID, err)
			return
		}

		kb, err := h.KBRepo.Get(pipeCtx, doc.KBID)
		if err != nil {
			logger.Errorf("get kb %d failed: %v", doc.KBID, err)
			return
		}

		switch kb.Mode {
		case "wiki":
			if h.WikiIngestPipe != nil {
				if err := h.WikiIngestPipe.Start(pipeCtx, doc.KBID, []int64{doc.ID}); err != nil {
					logger.Errorf("wiki ingest failed for doc %d: %v", event.DocID, err)
					_ = h.DocRepo.UpdateStatus(pipeCtx, doc.ID, domain.DocStatusFailed, err.Error())
				}
			}
		default:
			embedID := kb.EmbeddingModelID
			if embedID <= 0 {
				embedID = h.DefaultEmbeddingID
			}
			if err := h.IngestPipe.Run(pipeCtx, doc, kb.PipelineConfig, embedID, kb.OwnerID); err != nil {
				logger.Errorf("ingest pipeline failed for doc %d: %v", event.DocID, err)
				if h.IngestPipe.Progress != nil {
					h.IngestPipe.Progress(context.Background(), doc, eventpkg.RealtimeEvent{
						Type:    eventpkg.EventTypeKnowledgeFailed,
						Level:   eventpkg.EventLevelError,
						Title:   "处理失败",
						Message: "文档处理失败，请稍后重试",
					})
				}
			}
		}
	}()

	return nil
}
