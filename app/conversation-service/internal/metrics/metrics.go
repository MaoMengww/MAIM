package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	ConversationsCreatedTotal = metrics.NewCounterVec("conversations_created_total",
		"Total conversations created", "type")
	ConvMembersTotal = metrics.NewGaugeVec("conv_members_total",
		"Total conversation members")
)
