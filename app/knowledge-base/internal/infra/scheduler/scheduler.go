package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/eventpush"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	"github.com/maomeng/aim/pkg/logx"
)

// MaintenanceScheduler 管理所有知识库的自动维护定时任务
type MaintenanceScheduler struct {
	mu           sync.RWMutex
	cron         *cron.Cron
	entries      map[int64]cron.EntryID
	kbRepo       domain.KBRepo
	wikiMaintain *pipeline.WikiMaintenanceAgent
	logWriter    *pipeline.LogWriter
	logger       logx.Logger
	pusher       eventpush.Pusher
}

func New(kbRepo domain.KBRepo, wikiMaintain *pipeline.WikiMaintenanceAgent, logWriter *pipeline.LogWriter, logger logx.Logger) *MaintenanceScheduler {
	return &MaintenanceScheduler{
		cron:         cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor))),
		entries:      make(map[int64]cron.EntryID),
		kbRepo:       kbRepo,
		wikiMaintain: wikiMaintain,
		logWriter:    logWriter,
		logger:       logger,
	}
}

// WithPusher sets the realtime event pusher for maintenance completion notifications.
func (s *MaintenanceScheduler) WithPusher(pusher eventpush.Pusher) *MaintenanceScheduler {
	s.pusher = pusher
	return s
}

// Start 启动 cron 引擎
func (s *MaintenanceScheduler) Start() {
	s.cron.Start()
}

// Stop 停止所有定时器
func (s *MaintenanceScheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// ScheduleKB 为指定知识库注册定时维护（外部调用：启动时批量注册 + 更新时热更新）
func (s *MaintenanceScheduler) ScheduleKB(kb *domain.KnowledgeBase) {
	logger := s.logger
	s.removeEntry(kb.ID)
	if !kb.PipelineConfig.Wiki.MaintenanceEnabled || kb.PipelineConfig.Wiki.MaintenanceCron == "" {
		return
	}
	expr := kb.PipelineConfig.Wiki.MaintenanceCron
	id, err := s.cron.AddFunc(expr, func() {
		s.runMaintenance(kb.ID)
	})
	if err != nil {
		logger.Errorf("schedule kb %d cron %q failed: %v", kb.ID, expr, err)
		return
	}
	s.mu.Lock()
	s.entries[kb.ID] = id
	s.mu.Unlock()
	logger.Infof("wiki maintenance scheduled: kb=%d cron=%q", kb.ID, expr)
}

// UnscheduleKB 取消指定知识库的定时维护
func (s *MaintenanceScheduler) UnscheduleKB(kbID int64) {
	s.removeEntry(kbID)
}

func (s *MaintenanceScheduler) removeEntry(kbID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[kbID]; ok {
		s.cron.Remove(id)
		delete(s.entries, kbID)
	}
}

func (s *MaintenanceScheduler) runMaintenance(kbID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logger := s.logger.WithContext(ctx)
	logger.Infof("wiki maintenance triggered: kb=%d", kbID)

	report, err := s.wikiMaintain.Run(ctx, kbID)
	if report == nil {
		report = &pipeline.MaintenanceReport{KBID: kbID}
	}
	s.wikiMaintain.PushResult(ctx, kbID, report, err, "自动")
}

// ListSchedules 返回当前所有定时任务的 kbID 列表（调试用）
func (s *MaintenanceScheduler) ListSchedules() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int64, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	return ids
}
