package logic

import (
	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
)

func modelToPb(e *model.AuditEvent) *audit.AuditEvent {
	return &audit.AuditEvent{
		EventId:        e.EventID,
		Action:         audit.AuditAction(e.Action),
		Result:         audit.AuditResult(e.Result),
		Risk:           audit.AuditRisk(e.Risk),
		UserId:         e.UserID,
		DeviceId:       e.DeviceID,
		IpAddress:      e.IPAddress,
		UserAgent:      e.UserAgent,
		ResourceType:   e.ResourceType,
		ResourceId:     e.ResourceID,
		Detail:         "",
		ErrorMessage:   e.ErrorMessage,
		TraceId:        e.TraceID,
		SpanId:         e.SpanID,
		ServiceName:    e.ServiceName,
		ServiceVersion: e.ServiceVersion,
		CreatedAt:      e.CreatedAt.Unix(),
	}
}
