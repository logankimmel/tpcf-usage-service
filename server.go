package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// metricsHandler handles the /metrics endpoint
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

// refreshDataPeriodically runs in a goroutine to refresh data periodically
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

// runServer starts the HTTP server with metrics and health endpoints
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