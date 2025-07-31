package main

import (
	"fmt"
	"strings"
)

// formatPrometheusMetrics formats usage results as Prometheus metrics
func formatPrometheusMetrics(result *UsageResult) string {
	var metrics strings.Builder
	
	// Total metrics
	metrics.WriteString("# HELP cf_total_application_instances Total number of application instances across all organizations (includes system)\n")
	metrics.WriteString("# TYPE cf_total_application_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_total_application_instances %d\n", result.TotalAIs))
	
	metrics.WriteString("# HELP cf_total_billable_application_instances Total number of billable application instances (excludes system org)\n")
	metrics.WriteString("# TYPE cf_total_billable_application_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_total_billable_application_instances %d\n", result.TotalBillableAIs))
	
	metrics.WriteString("# HELP cf_total_service_instances Total number of service instances across all organizations\n")
	metrics.WriteString("# TYPE cf_total_service_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_total_service_instances %d\n", result.TotalSIs))
	
	metrics.WriteString("# HELP cf_total_billable_service_instances Total number of billable service instances across all organizations\n")
	metrics.WriteString("# TYPE cf_total_billable_service_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_total_billable_service_instances %d\n", result.TotalBillableSIs))
	
	metrics.WriteString("# HELP cf_monthly_max_billable_application_instances Maximum billable application instances this month\n")
	metrics.WriteString("# TYPE cf_monthly_max_billable_application_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_monthly_max_billable_application_instances %d\n", result.MonthlyMaxBillableAIs))
	
	metrics.WriteString("# HELP cf_yearly_max_billable_application_instances Maximum billable application instances this year\n")
	metrics.WriteString("# TYPE cf_yearly_max_billable_application_instances gauge\n")
	metrics.WriteString(fmt.Sprintf("cf_yearly_max_billable_application_instances %d\n", result.YearlyMaxBillableAIs))
	
	// Per-organization metrics
	metrics.WriteString("# HELP cf_org_application_instances Number of application instances per organization\n")
	metrics.WriteString("# TYPE cf_org_application_instances gauge\n")
	for _, org := range result.Organizations {
		metrics.WriteString(fmt.Sprintf("cf_org_application_instances{org=\"%s\"} %d\n", org.Name, org.AIs))
	}
	
	metrics.WriteString("# HELP cf_org_service_instances Number of service instances per organization\n")
	metrics.WriteString("# TYPE cf_org_service_instances gauge\n")
	for _, org := range result.Organizations {
		metrics.WriteString(fmt.Sprintf("cf_org_service_instances{org=\"%s\"} %d\n", org.Name, org.SIs))
	}
	
	metrics.WriteString("# HELP cf_org_billable_service_instances Number of billable service instances per organization\n")
	metrics.WriteString("# TYPE cf_org_billable_service_instances gauge\n")
	for _, org := range result.Organizations {
		metrics.WriteString(fmt.Sprintf("cf_org_billable_service_instances{org=\"%s\"} %d\n", org.Name, org.BillableSIs))
	}
	
	return metrics.String()
}