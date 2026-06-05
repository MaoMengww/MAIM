package logic

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/app/audit-service/internal/repo"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"

	"github.com/zeromicro/go-zero/core/logx"
)

type ExportAuditsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewExportAuditsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ExportAuditsLogic {
	return &ExportAuditsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ExportAuditsLogic) ExportAudits(in *audit.ExportAuditsReq) (*audit.ExportAuditsResp, error) {
	logger := l.WithContext(l.ctx)

	maxEvents := int(in.MaxEvents)
	if maxEvents <= 0 || maxEvents > 10000 {
		maxEvents = 10000
	}

	f := repo.ListFilter{
		PageSize: maxEvents,
		Page:     1,
	}
	if in.UserId != nil {
		f.UserID = in.UserId
	}
	if in.Action != nil {
		v := int32(*in.Action)
		f.Action = &v
	}
	if in.Result != nil {
		v := int32(*in.Result)
		f.Result = &v
	}
	if in.StartTime != nil {
		f.StartTime = in.StartTime
	}
	if in.EndTime != nil {
		f.EndTime = in.EndTime
	}
	if in.ServiceName != nil {
		f.ServiceName = in.ServiceName
	}

	events, err := l.svcCtx.AuditRepo.Export(l.ctx, f)
	if err != nil {
		logger.Errorf("export audit events failed: err=%v", err)
		return nil, err
	}

	if len(events) == 0 {
		return &audit.ExportAuditsResp{EventCount: 0}, nil
	}

	now := time.Now()
	dateDir := now.Format("2006-01-02")
	fileName := fmt.Sprintf("export_%s_%d.%s", now.Format("20060102_150405"), len(events), formatExt(in.Format))
	objectKey := fmt.Sprintf("audit/exports/%s/%s", dateDir, fileName)

	content, err := formatExport(events, in.Format)
	if err != nil {
		logger.Errorf("format export failed: err=%v", err)
		return nil, err
	}

	if l.svcCtx.MinIO != nil {
		data := []byte(content)
		contentType := contentTypeForFormat(in.Format)
		if _, err := l.svcCtx.MinIO.Upload(l.ctx, objectKey, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
			logger.Errorf("upload export file failed: err=%v", err)
			return nil, err
		}

		logger.Infof("audit export completed: file=%s count=%d", objectKey, len(events))
		return &audit.ExportAuditsResp{
			FileUrl:    l.svcCtx.MinIO.PublicURL(objectKey),
			EventCount: int32(len(events)),
			ExpiresAt:  0,
		}, nil
	}

	logger.Infof("audit export completed (no minio): count=%d", len(events))
	return &audit.ExportAuditsResp{
		FileUrl:    "",
		EventCount: int32(len(events)),
		ExpiresAt:  0,
	}, nil
}

func contentTypeForFormat(f audit.ExportFormat) string {
	switch f {
	case audit.ExportFormat_EXPORT_FORMAT_CSV:
		return "text/csv"
	default:
		return "application/json"
	}
}

func formatExt(f audit.ExportFormat) string {
	switch f {
	case audit.ExportFormat_EXPORT_FORMAT_JSON:
		return "json"
	case audit.ExportFormat_EXPORT_FORMAT_CSV:
		return "csv"
	case audit.ExportFormat_EXPORT_FORMAT_JSONL:
		return "jsonl"
	default:
		return "json"
	}
}

func formatExport(events []model.AuditEvent, f audit.ExportFormat) (string, error) {
	switch f {
	case audit.ExportFormat_EXPORT_FORMAT_CSV:
		return buildCSV(events)
	case audit.ExportFormat_EXPORT_FORMAT_JSONL:
		return buildJSONL(events)
	default:
		return buildJSON(events)
	}
}

func buildJSON(events []model.AuditEvent) (string, error) {
	b, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func buildJSONL(events []model.AuditEvent) (string, error) {
	var sb strings.Builder
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			return "", err
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func buildCSV(events []model.AuditEvent) (string, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	w.Write([]string{"event_id", "action", "result", "risk", "user_id", "device_id", "ip_address", "user_agent",
		"resource_type", "resource_id", "detail", "error_message", "trace_id", "span_id",
		"service_name", "service_version", "created_at"})

	for _, e := range events {
		detail, _ := e.Detail.Value()
		w.Write([]string{
			e.EventID,
			fmt.Sprintf("%d", e.Action),
			fmt.Sprintf("%d", e.Result),
			fmt.Sprintf("%d", e.Risk),
			fmt.Sprintf("%d", e.UserID),
			e.DeviceID,
			e.IPAddress,
			e.UserAgent,
			e.ResourceType,
			e.ResourceID,
			string(detail.([]byte)),
			e.ErrorMessage,
			e.TraceID,
			e.SpanID,
			e.ServiceName,
			e.ServiceVersion,
			e.CreatedAt.Format(time.RFC3339),
		})
	}
	w.Flush()
	return buf.String(), nil
}
