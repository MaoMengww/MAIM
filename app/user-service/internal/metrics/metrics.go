package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	UserRegistrationsTotal = metrics.NewCounterVec("user_registrations_total",
		"Total user registrations", "source")
	UserLoginsTotal = metrics.NewCounterVec("user_logins_total",
		"Total user logins", "source")
	RechargeAmountTotal = metrics.NewCounterVec("recharge_amount_total",
		"Total recharge amount (cents)", "currency")
	RechargeCountTotal = metrics.NewCounterVec("recharge_count_total",
		"Total recharge transactions", "channel")
	ConsumptionAmountTotal = metrics.NewCounterVec("consumption_amount_total",
		"Total consumption amount (cents)", "category")
	ConsumptionCountTotal = metrics.NewCounterVec("consumption_count_total",
		"Total consumption transactions", "category")
	UserBalanceGauge = metrics.NewGaugeVec("user_balance",
		"User balance distribution buckets", "bucket")
)
