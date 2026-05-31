package api

import "time"

type OpenCodeQuotaFormat string

const (
	OpenCodeQuotaFormatCurrency OpenCodeQuotaFormat = "currency"
	OpenCodeQuotaFormatPercent  OpenCodeQuotaFormat = "percent"
)

type OpenCodeQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      OpenCodeQuotaFormat
	ResetsAt    *time.Time
}

type OpenCodeAccountType string

const (
	OpenCodeAccountTypePro OpenCodeAccountType = "pro"
)

type OpenCodeSnapshot struct {
	ID          int64
	CapturedAt  time.Time
	RawJSON     string
	AccountType OpenCodeAccountType
	PlanName    string
	Quotas      []OpenCodeQuota
}
