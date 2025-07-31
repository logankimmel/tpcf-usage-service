package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// parseFlags parses command line flags and returns configuration
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
		if result.MonthlyMaxBillableAIs > 0 || result.YearlyMaxBillableAIs > 0 {
			fmt.Printf("Monthly Max Billable AIs: %d\n", result.MonthlyMaxBillableAIs)
			fmt.Printf("Yearly Max Billable AIs: %d\n", result.YearlyMaxBillableAIs)
		} else if config.Verbose {
			fmt.Printf("Monthly/Yearly max data: Not available (app-usage service not deployed)\n")
		}
	}
}