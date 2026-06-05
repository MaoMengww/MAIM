package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	// 已有
	MessagesSentTotal = metrics.NewCounterVec("messages_sent_total",
		"Total messages sent", "msg_type")

	// 新增
	MessageEditRecalledTotal = metrics.NewCounterVec("message_edit_recalled_total",
		"Message edit/recall count", "action")
)
