package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

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

type CFClient struct {
	httpClient       *http.Client
	servicePlans     map[string]ServicePlan
	serviceOfferings map[string]ServiceOffering
	apiEndpoint      string
	accessToken      string
}

func NewCFClient() (*CFClient, error) {
	// Check for SSL verification skip
	skipSSLVerification := os.Getenv("CF_SKIP_SSL_VALIDATION") == "true"
	
	// Configure HTTP client with optional SSL skip
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: skipSSLVerification,
			},
		},
	}
	
	if skipSSLVerification {
		log.Printf("WARNING: SSL certificate verification is disabled")
	}
	
	client := &CFClient{
		httpClient:       httpClient,
		servicePlans:     make(map[string]ServicePlan),
		serviceOfferings: make(map[string]ServiceOffering),
	}
	
	// Check for environment variables for direct API access
	apiEndpoint := os.Getenv("CF_API_ENDPOINT")
	username := os.Getenv("CF_USERNAME")
	password := os.Getenv("CF_PASSWORD")
	
	if apiEndpoint != "" && username != "" && password != "" {
		if err := client.authenticateWithCredentials(apiEndpoint, username, password); err != nil {
			return nil, fmt.Errorf("failed to authenticate with CF API: %w", err)
		}
		log.Printf("Using environment variable credentials with direct API calls")
	} else {
		return nil, fmt.Errorf("CF_API_ENDPOINT, CF_USERNAME, and CF_PASSWORD environment variables are required")
	}
	
	return client, nil
}


func (c *CFClient) discoverAuthEndpoint(apiEndpoint string) (string, error) {
	infoURL := apiEndpoint + "/v2/info"
	
	req, err := http.NewRequest("GET", infoURL, nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Accept", "application/json")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get API info: status %d", resp.StatusCode)
	}
	
	var info struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	
	if info.AuthorizationEndpoint == "" {
		return "", fmt.Errorf("no authorization endpoint found in API info")
	}
	
	// Convert authorization endpoint to token endpoint
	authEndpoint := strings.TrimSuffix(info.AuthorizationEndpoint, "/")
	tokenEndpoint := authEndpoint + "/oauth/token"
	
	log.Printf("Discovered OAuth token endpoint: %s", tokenEndpoint)
	return tokenEndpoint, nil
}

func (c *CFClient) authenticateDirectly(tokenURL, username, password string) error {
	// Standard CF client credentials - try the most common one first
	clientID := "cf"
	clientSecret := ""
	
	// Check for custom client credentials from environment
	if envClientID := os.Getenv("CF_CLIENT_ID"); envClientID != "" {
		clientID = envClientID
		clientSecret = os.Getenv("CF_CLIENT_SECRET")
		log.Printf("Using custom OAuth client credentials from environment")
	}
	
	// Prepare the request payload
	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("username", username)
	data.Set("password", password)
	
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	
	// Add client credentials if we have them
	if clientSecret != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
		req.Header.Set("Authorization", "Basic "+credentials)
	} else {
		// Standard CF CLI client (public client)
		credentials := base64.StdEncoding.EncodeToString([]byte(clientID + ":"))
		req.Header.Set("Authorization", "Basic "+credentials)
	}
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}
	
	c.accessToken = tokenResp.AccessToken
	log.Printf("Successfully authenticated via direct OAuth")
	return nil
}

func (c *CFClient) authenticateWithCredentials(apiEndpoint, username, password string) error {
	c.apiEndpoint = strings.TrimSuffix(apiEndpoint, "/")
	
	// Discover the OAuth endpoint
	tokenURL, err := c.discoverAuthEndpoint(c.apiEndpoint)
	if err != nil {
		log.Printf("Failed to discover OAuth endpoint: %v", err)
		
		// Try common endpoints as fallback
		fallbackURLs := []string{
			strings.Replace(apiEndpoint, "api.", "uaa.", 1) + "/oauth/token", // UAA endpoint
			apiEndpoint + "/uaa/oauth/token",                                  // Newer CF/TAS
			apiEndpoint + "/oauth/token",                                      // Older CF
		}
		
		for _, fallbackURL := range fallbackURLs {
			log.Printf("Trying fallback endpoint: %s", fallbackURL)
			if err := c.authenticateDirectly(fallbackURL, username, password); err == nil {
				return nil
			} else {
				log.Printf("Fallback endpoint failed: %v", err)
			}
		}
		
		return fmt.Errorf("authentication failed at all endpoints")
	}
	
	// Try the discovered endpoint
	return c.authenticateDirectly(tokenURL, username, password)
}


func (c *CFClient) apiCall(endpoint string) ([]byte, error) {
	return c.directAPICall(endpoint)
}

func (c *CFClient) directAPICall(endpoint string) ([]byte, error) {
	url := c.apiEndpoint + endpoint
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API call failed with status %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	return body, nil
}


func (c *CFClient) loadAllPages(url string) ([]json.RawMessage, error) {
	var allResources []json.RawMessage
	
	for url != "" && url != "null" {
		data, err := c.apiCall(url)
		if err != nil {
			return nil, err
		}
		
		var resp CFResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		
		allResources = append(allResources, resp.Resources...)
		
		if resp.Pagination.Next.Href == "" {
			break
		}
		
		// Strip domain prefix for cf curl compatibility
		url = resp.Pagination.Next.Href
		if strings.Contains(url, "://") {
			parts := strings.Split(url, "/")
			if len(parts) >= 4 {
				url = "/" + strings.Join(parts[3:], "/")
			}
		}
	}
	
	return allResources, nil
}

func (c *CFClient) getOrganizations() ([]Organization, error) {
	resources, err := c.loadAllPages("/v3/organizations?per_page=1000")
	if err != nil {
		return nil, err
	}
	
	var orgs []Organization
	for _, resource := range resources {
		var org Organization
		if err := json.Unmarshal(resource, &org); err != nil {
			return nil, fmt.Errorf("failed to parse organization: %w", err)
		}
		orgs = append(orgs, org)
	}
	
	return orgs, nil
}

func (c *CFClient) loadServicePlans() error {
	resources, err := c.loadAllPages("/v3/service_plans?per_page=1000")
	if err != nil {
		return fmt.Errorf("failed to load service plans: %w", err)
	}
	
	for _, resource := range resources {
		var plan ServicePlan
		if err := json.Unmarshal(resource, &plan); err != nil {
			return fmt.Errorf("failed to parse service plan: %w", err)
		}
		c.servicePlans[plan.GUID] = plan
	}
	
	return nil
}

func (c *CFClient) loadServiceOfferings() error {
	resources, err := c.loadAllPages("/v3/service_offerings?per_page=1000")
	if err != nil {
		return fmt.Errorf("failed to load service offerings: %w", err)
	}
	
	for _, resource := range resources {
		var offering ServiceOffering
		if err := json.Unmarshal(resource, &offering); err != nil {
			return fmt.Errorf("failed to parse service offering: %w", err)
		}
		c.serviceOfferings[offering.GUID] = offering
	}
	
	return nil
}

func (c *CFClient) getServiceInstances(orgGUID string) ([]ServiceInstance, error) {
	endpoint := fmt.Sprintf("/v3/service_instances?organization_guids=%s&per_page=1000", orgGUID)
	resources, err := c.loadAllPages(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load service instances: %w", err)
	}
	
	var instances []ServiceInstance
	for _, resource := range resources {
		var instance ServiceInstance
		if err := json.Unmarshal(resource, &instance); err != nil {
			return nil, fmt.Errorf("failed to parse service instance: %w", err)
		}
		instances = append(instances, instance)
	}
	
	return instances, nil
}

func (c *CFClient) isServiceInstanceBillable(instance ServiceInstance) (bool, string) {
	planGUID := instance.Relationships.ServicePlan.Data.GUID
	plan, exists := c.servicePlans[planGUID]
	if !exists {
		return false, "unknown-plan"
	}
	
	offeringGUID := plan.Relationships.ServiceOffering.Data.GUID
	offering, exists := c.serviceOfferings[offeringGUID]
	if !exists {
		return false, "unknown-offering"
	}
	
	// Billable service offerings
	billableOfferings := map[string]bool{
		"p.mysql":      true,
		"p-mysql":      true,
		"p.rabbitmq":   true,
		"p-rabbitmq":   true,
		"p.redis":      true,
		"p-redis":      true,
		"postgres":     true,
		"genai":        true,
		"genai-service": true,
	}
	
	return billableOfferings[offering.Name], offering.Name
}

func (c *CFClient) getUsageSummary(orgGUID string) (*UsageSummary, error) {
	endpoint := fmt.Sprintf("/v3/organizations/%s/usage_summary", orgGUID)
	data, err := c.apiCall(endpoint)
	if err != nil {
		return nil, err
	}
	
	var summary UsageSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("failed to parse usage summary: %w", err)
	}
	
	return &summary, nil
}

func (c *CFClient) getAppUsageReport() (*AppUsageReport, error) {
	// The app-usage endpoint needs to be constructed from the API endpoint
	// Replace api.sys.domain with app-usage.sys.domain
	appUsageEndpoint := strings.Replace(c.apiEndpoint, "api.", "app-usage.", 1)
	reportURL := appUsageEndpoint + "/system_report/app_usages"
	
	req, err := http.NewRequest("GET", reportURL, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("app usage report request failed with status %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	var report AppUsageReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("failed to parse app usage report: %w", err)
	}
	
	return &report, nil
}

type Config struct {
	SkipOrgs        []string
	Verbose         bool
	JSONOutput      bool
	ServerMode      bool
	Port            int
	RefreshInterval time.Duration
}

type UsageResult struct {
	Organizations         []OrgUsage `json:"organizations,omitempty"`
	TotalAIs              int        `json:"total_ais"`              // Includes all orgs including system
	TotalBillableAIs      int        `json:"total_billable_ais"`     // Excludes system org
	TotalSIs              int        `json:"total_sis"`
	TotalBillableSIs      int        `json:"total_billable_sis"`
	MonthlyMaxBillableAIs int        `json:"monthly_max_billable_ais"` // Maximum billable AIs this month
	YearlyMaxBillableAIs  int        `json:"yearly_max_billable_ais"`  // Maximum billable AIs this year
}

type OrgUsage struct {
	Name       string `json:"name"`
	AIs        int    `json:"ais"`
	SIs        int    `json:"sis"`
	BillableSIs int   `json:"billable_sis"`
}

func parseFlags() *Config {
	config := &Config{}
	
	var skipOrgs string
	var refreshMinutes int
	flag.StringVar(&skipOrgs, "skip-orgs", "system", "Comma-separated list of orgs to skip")
	flag.BoolVar(&config.Verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&config.JSONOutput, "json", false, "Output results as JSON")
	flag.BoolVar(&config.ServerMode, "server", false, "Run as web server with Prometheus metrics endpoint")
	flag.IntVar(&config.Port, "port", 8080, "Port to run web server on (only used with -server)")
	flag.IntVar(&refreshMinutes, "refresh-interval", 60, "Data refresh interval in minutes for server mode (default: 60)")
	flag.Parse()
	
	config.RefreshInterval = time.Duration(refreshMinutes) * time.Minute
	
	if skipOrgs != "" {
		config.SkipOrgs = strings.Split(skipOrgs, ",")
		for i := range config.SkipOrgs {
			config.SkipOrgs[i] = strings.TrimSpace(config.SkipOrgs[i])
		}
	}
	
	return config
}

func shouldSkipOrg(orgName string, skipList []string) bool {
	for _, skip := range skipList {
		if orgName == skip {
			return true
		}
	}
	return false
}

func collectUsageData(client *CFClient, config *Config) (*UsageResult, error) {
	orgs, err := client.getOrganizations()
	if err != nil {
		return nil, fmt.Errorf("failed to get organizations: %w", err)
	}
	
	totalAIs := 0              // Includes ALL orgs (including system)
	totalBillableAIs := 0      // Excludes system org
	totalSIs := 0
	totalBillableSIs := 0
	var orgUsages []OrgUsage
	
	for _, org := range orgs {
		if config.Verbose {
			fmt.Printf("Processing %s...\n", org.Name)
		}
		
		summary, err := client.getUsageSummary(org.GUID)
		if err != nil {
			log.Printf("Failed to get usage summary for org %s: %v", org.Name, err)
			continue
		}
		
		ais := summary.UsageSummary.StartedInstances
		sis := summary.UsageSummary.ServiceInstances
		
		// Always count AIs for total (including system org)
		totalAIs += ais
		totalSIs += sis
		
		// Skip further processing for organizations in skip list
		if shouldSkipOrg(org.Name, config.SkipOrgs) {
			if config.Verbose {
				log.Printf("Skipping org for billable counts: %s", org.Name)
			}
			continue
		}
		
		// Count billable AIs (excludes system org)
		totalBillableAIs += ais
		
		instances, err := client.getServiceInstances(org.GUID)
		if err != nil {
			log.Printf("Failed to get service instances for org %s: %v", org.Name, err)
			continue
		}
		
		billableSIs := 0
		for _, instance := range instances {
			if billable, offeringName := client.isServiceInstanceBillable(instance); billable {
				billableSIs++
				if config.Verbose {
					fmt.Printf("  Billable SI: %s (%s)\n", instance.Name, offeringName)
				}
			} else if config.Verbose {
				fmt.Printf("  Non-billable SI: %s (%s)\n", instance.Name, offeringName)
			}
		}
		
		totalBillableSIs += billableSIs
		
		orgUsages = append(orgUsages, OrgUsage{
			Name:       org.Name,
			AIs:        ais,
			SIs:        sis,
			BillableSIs: billableSIs,
		})
	}
	
	// Fetch monthly max billable AIs from app usage report
	monthlyMaxBillableAIs := 0
	yearlyMaxBillableAIs := 0
	
	if appReport, err := client.getAppUsageReport(); err != nil {
		log.Printf("Failed to get app usage report: %v", err)
		// Continue without monthly/yearly max data
	} else {
		// Get current month's max instances
		currentTime := time.Now()
		currentMonth := int(currentTime.Month())
		currentYear := currentTime.Year()
		
		for _, monthlyReport := range appReport.MonthlyReports {
			if monthlyReport.Month == currentMonth && monthlyReport.Year == currentYear {
				monthlyMaxBillableAIs = monthlyReport.MaximumAppInstances
				break
			}
		}
		
		// Get current year's max instances
		for _, yearlyReport := range appReport.YearlyReports {
			if yearlyReport.Year == currentYear {
				yearlyMaxBillableAIs = yearlyReport.MaximumAppInstances
				break
			}
		}
		
		if config.Verbose {
			log.Printf("Monthly max billable AIs: %d, Yearly max billable AIs: %d", monthlyMaxBillableAIs, yearlyMaxBillableAIs)
		}
	}
	
	return &UsageResult{
		Organizations:         orgUsages,
		TotalAIs:             totalAIs,
		TotalBillableAIs:     totalBillableAIs,
		TotalSIs:             totalSIs,
		TotalBillableSIs:     totalBillableSIs,
		MonthlyMaxBillableAIs: monthlyMaxBillableAIs,
		YearlyMaxBillableAIs:  yearlyMaxBillableAIs,
	}, nil
}

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

func metricsHandler(cachedData *CachedData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Metrics request from %s", r.RemoteAddr)
		
		result := cachedData.Get()
		if result == nil {
			log.Printf("No cached data available")
			http.Error(w, "No data available", http.StatusServiceUnavailable)
			return
		}
		
		metrics := formatPrometheusMetrics(result)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(metrics))
	}
}

func refreshDataPeriodically(client *CFClient, config *Config, cachedData *CachedData, stopChan <-chan struct{}) {
	ticker := time.NewTicker(config.RefreshInterval)
	defer ticker.Stop()
	
	// Initial data fetch
	log.Printf("Performing initial data fetch...")
	if result, err := collectUsageData(client, config); err != nil {
		log.Printf("Initial data fetch failed: %v", err)
	} else {
		cachedData.Set(result)
		log.Printf("Initial data fetch completed successfully")
	}
	
	for {
		select {
		case <-ticker.C:
			log.Printf("Refreshing data...")
			if result, err := collectUsageData(client, config); err != nil {
				log.Printf("Data refresh failed: %v", err)
			} else {
				cachedData.Set(result)
				if config.Verbose {
					log.Printf("Data refreshed successfully - Total AIs: %d (Billable: %d), Total SIs: %d (Billable: %d)", 
						result.TotalAIs, result.TotalBillableAIs, result.TotalSIs, result.TotalBillableSIs)
				} else {
					log.Printf("Data refreshed successfully")
				}
			}
		case <-stopChan:
			log.Printf("Stopping data refresh...")
			return
		}
	}
}

func runServer(client *CFClient, config *Config) {
	cachedData := &CachedData{}
	stopChan := make(chan struct{})
	
	// Start background data refresh
	go refreshDataPeriodically(client, config, cachedData, stopChan)
	
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", metricsHandler(cachedData))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(config.Port),
		Handler: mux,
	}
	
	// Graceful shutdown handling
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		
		log.Println("Shutting down server...")
		close(stopChan) // Stop data refresh
		
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()
	
	log.Printf("Starting server on port %d", config.Port)
	log.Printf("Data refresh interval: %v", config.RefreshInterval)
	log.Printf("Metrics endpoint: http://localhost:%d/metrics", config.Port)
	log.Printf("Health endpoint: http://localhost:%d/health", config.Port)
	
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func main() {
	config := parseFlags()
	
	// Override config with environment variables for CF deployment
	if os.Getenv("TPCF_SERVER_MODE") == "true" {
		config.ServerMode = true
	}
	if os.Getenv("TPCF_VERBOSE") == "true" {
		config.Verbose = true
	}
	if portStr := os.Getenv("PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	}
	
	client, err := NewCFClient()
	if err != nil {
		log.Fatalf("Failed to create CF client: %v", err)
	}
	
	// Load service plans and offerings for billable service instance detection
	if config.Verbose {
		fmt.Println("Loading service plans and offerings...")
	}
	if err := client.loadServicePlans(); err != nil {
		log.Fatalf("Failed to load service plans: %v", err)
	}
	if err := client.loadServiceOfferings(); err != nil {
		log.Fatalf("Failed to load service offerings: %v", err)
	}
	
	if config.ServerMode {
		runServer(client, config)
		return
	}
	
	// CLI mode - collect and display data once
	result, err := collectUsageData(client, config)
	if err != nil {
		log.Fatalf("Failed to collect usage data: %v", err)
	}
	
	if config.JSONOutput {
		output, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal JSON: %v", err)
		}
		fmt.Println(string(output))
	} else {
		for _, org := range result.Organizations {
			fmt.Printf("Processing %s...\n", org.Name)
			fmt.Printf("AIs: %d\n", org.AIs)
			fmt.Printf("SIs: %d (Billable: %d)\n", org.SIs, org.BillableSIs)
			fmt.Println()
		}
		fmt.Printf("Total AIs: %d (Billable: %d)\n", result.TotalAIs, result.TotalBillableAIs)
		fmt.Printf("Total SIs: %d (Billable: %d)\n", result.TotalSIs, result.TotalBillableSIs)
		fmt.Printf("Monthly Max Billable AIs: %d\n", result.MonthlyMaxBillableAIs)
		fmt.Printf("Yearly Max Billable AIs: %d\n", result.YearlyMaxBillableAIs)
	}
}