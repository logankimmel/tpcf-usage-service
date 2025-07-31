package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// NewCFClient creates a new CF client with authentication
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

// Authentication methods
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

// API call methods
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

// CF API resource methods
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
		
		// Strip domain prefix for API compatibility
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
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("app-usage service not found (not deployed in this foundation)")
		}
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

// Usage data collection
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
		if config.Verbose {
			log.Printf("App usage report not available (this is normal if app-usage service is not deployed): %v", err)
		}
		// Continue without monthly/yearly max data - this is expected in many foundations
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

// Helper functions
func shouldSkipOrg(orgName string, skipList []string) bool {
	for _, skip := range skipList {
		if orgName == skip {
			return true
		}
	}
	return false
}