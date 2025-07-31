package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// CF API Response Types
type CFResponse struct {
	Resources  []json.RawMessage `json:"resources"`
	Pagination struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"pagination"`
}

type Organization struct {
	Name string `json:"name"`
	GUID string `json:"guid"`
}

type ServiceInstance struct {
	GUID          string `json:"guid"`
	Name          string `json:"name"`
	Relationships struct {
		ServicePlan struct {
			Data struct {
				GUID string `json:"guid"`
			} `json:"data"`
		} `json:"service_plan"`
	} `json:"relationships"`
}

type ServicePlan struct {
	GUID          string `json:"guid"`
	Name          string `json:"name"`
	Relationships struct {
		ServiceOffering struct {
			Data struct {
				GUID string `json:"guid"`
			} `json:"data"`
		} `json:"service_offering"`
	} `json:"relationships"`
}

type ServiceOffering struct {
	GUID string `json:"guid"`
	Name string `json:"name"`
}

type UsageSummary struct {
	UsageSummary struct {
		StartedInstances  int `json:"started_instances"`
		ServiceInstances int `json:"service_instances"`
	} `json:"usage_summary"`
}

// App Usage Report Types
type AppUsageReport struct {
	ReportTime      string          `json:"report_time"`
	MonthlyReports  []MonthlyReport `json:"monthly_reports"`
	YearlyReports   []YearlyReport  `json:"yearly_reports"`
}

type MonthlyReport struct {
	Month                int     `json:"month"`
	Year                 int     `json:"year"`
	AverageAppInstances  float64 `json:"average_app_instances"`
	MaximumAppInstances  int     `json:"maximum_app_instances"`
	AppInstanceHours     float64 `json:"app_instance_hours"`
}

type YearlyReport struct {
	Year                int     `json:"year"`
	AverageAppInstances float64 `json:"average_app_instances"`
	MaximumAppInstances int     `json:"maximum_app_instances"`
	AppInstanceHours    float64 `json:"app_instance_hours"`
}

// CF Client
type CFClient struct {
	httpClient       *http.Client
	servicePlans     map[string]ServicePlan
	serviceOfferings map[string]ServiceOffering
	apiEndpoint      string
	accessToken      string
}

// Configuration
type Config struct {
	SkipOrgs        []string
	Verbose         bool
	JSONOutput      bool
	ServerMode      bool
	Port            int
	RefreshInterval time.Duration
}

// Usage Results
type UsageResult struct {
	Organizations         []OrgUsage `json:"organizations,omitempty"`
	TotalAIs              int        `json:"total_ais"`                 // Includes all orgs including system
	TotalBillableAIs      int        `json:"total_billable_ais"`        // Excludes system org
	TotalSIs              int        `json:"total_sis"`
	TotalBillableSIs      int        `json:"total_billable_sis"`
	MonthlyMaxBillableAIs int        `json:"monthly_max_billable_ais"`  // Maximum billable AIs this month
	YearlyMaxBillableAIs  int        `json:"yearly_max_billable_ais"`   // Maximum billable AIs this year
}

type OrgUsage struct {
	Name       string `json:"name"`
	AIs        int    `json:"ais"`
	SIs        int    `json:"sis"`
	BillableSIs int   `json:"billable_sis"`
}

// Server Types
type CachedData struct {
	Result    *UsageResult
	LastFetch time.Time
	mu        sync.RWMutex
}

func (cd *CachedData) Get() *UsageResult {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	return cd.Result
}

func (cd *CachedData) Set(result *UsageResult) {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.Result = result
	cd.LastFetch = time.Now()
}

func (cd *CachedData) IsStale(refreshInterval time.Duration) bool {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	return cd.Result == nil || time.Since(cd.LastFetch) > refreshInterval
}