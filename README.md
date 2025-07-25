# CF Usage Service

A Go application that connects to the Cloud Foundry API to count billable Application Instances (AIs) and Service Instances (SIs) across organizations. The application identifies truly billable service instances by analyzing service plans and offerings, filtering out non-billable services like user-provided services.

Can run as a CLI tool for one-time reporting or as a web server exposing Prometheus metrics.

## Prerequisites

- Go 1.19 or later
- CF CLI installed and authenticated to your Cloud Foundry foundation

## Building

```bash
go build -o cf-usage-service
```

## Usage

### CLI Mode (One-time reporting)

```bash
# Basic usage (skips 'system' org by default)
./cf-usage-service

# Skip multiple orgs
./cf-usage-service --skip-orgs "system,another-org"

# Verbose output
./cf-usage-service --verbose

# JSON output for automation
./cf-usage-service --json

# JSON output with specific orgs skipped
./cf-usage-service --json --skip-orgs "system,test-org"
```

### Server Mode (Prometheus metrics)

```bash
# Start web server on default port 8080 (refreshes every hour)
./cf-usage-service --server

# Start on custom port
./cf-usage-service --server --port 9090

# Custom refresh interval (refresh every 30 minutes)
./cf-usage-service --server --refresh-interval 30

# Server with verbose logging
./cf-usage-service --server --verbose
```

**Endpoints:**
- `GET /metrics` - Prometheus metrics endpoint (returns cached data)
- `GET /health` - Health check endpoint

**Server Behavior:**
- Fetches data from Cloud Foundry API on startup and then periodically based on `--refresh-interval`
- Metrics endpoint returns cached data (no API calls on each request)
- Background refresh continues until server shutdown

## Options

- `--skip-orgs`: Comma-separated list of organizations to skip (default: "system")
- `--verbose`: Enable verbose output showing processing details
- `--json`: Output results in JSON format for automation/scripting (CLI mode only)
- `--server`: Run as web server with Prometheus metrics endpoint
- `--port`: Port to run web server on (default: 8080, only used with --server)
- `--refresh-interval`: Data refresh interval in minutes for server mode (default: 60)

## Example Output

### Standard Output
```
Loading service plans and offerings...
Processing my-org...
AIs: 25
SIs: 12 (Billable: 8)

Processing another-org...
AIs: 8
SIs: 5 (Billable: 3)

Total AIs: 45 (Billable: 33)
Total SIs: 17 (Billable: 11)
```

### JSON Output
```json
{
  "organizations": [
    {
      "name": "my-org",
      "ais": 25,
      "sis": 12,
      "billable_sis": 8
    },
    {
      "name": "another-org", 
      "ais": 8,
      "sis": 5,
      "billable_sis": 3
    }
  ],
  "total_ais": 45,
  "total_billable_ais": 33,
  "total_sis": 17,
  "total_billable_sis": 11
}
```

### Prometheus Metrics Output

When running in server mode, the `/metrics` endpoint provides:

```
# HELP cf_total_application_instances Total number of application instances across all organizations (includes system)
# TYPE cf_total_application_instances gauge
cf_total_application_instances 45

# HELP cf_total_billable_application_instances Total number of billable application instances (excludes system org)
# TYPE cf_total_billable_application_instances gauge
cf_total_billable_application_instances 33

# HELP cf_total_service_instances Total number of service instances across all organizations  
# TYPE cf_total_service_instances gauge
cf_total_service_instances 17

# HELP cf_total_billable_service_instances Total number of billable service instances across all organizations
# TYPE cf_total_billable_service_instances gauge
cf_total_billable_service_instances 11

# HELP cf_org_application_instances Number of application instances per organization
# TYPE cf_org_application_instances gauge
cf_org_application_instances{org="my-org"} 25
cf_org_application_instances{org="another-org"} 8

# HELP cf_org_service_instances Number of service instances per organization
# TYPE cf_org_service_instances gauge
cf_org_service_instances{org="my-org"} 12
cf_org_service_instances{org="another-org"} 5

# HELP cf_org_billable_service_instances Number of billable service instances per organization
# TYPE cf_org_billable_service_instances gauge
cf_org_billable_service_instances{org="my-org"} 8
cf_org_billable_service_instances{org="another-org"} 3
```

## Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'cf-usage'
    static_configs:
      - targets: ['localhost:8080']
    scrape_interval: 300s  # 5 minutes (should be less than refresh interval)
    metrics_path: '/metrics'
```

## Migration from Bash Script

This application replaces the original `get-usage-count.sh` bash script with:

- **Accurate billable service instance counting** via service plan/offering analysis
- Better error handling and logging
- Configurable organization filtering
- JSON output for automation
- Proper Go module structure
- Cross-platform compatibility

## Billable Service Detection

The application determines which service instances are billable by:

1. Fetching all service plans from `/v3/service_plans`
2. Fetching all service offerings from `/v3/service_offerings`
3. For each service instance, looking up its service plan GUID
4. Using the plan's service offering GUID to identify the offering name
5. Filtering out known non-billable offerings (e.g., "user-provided", "app-autoscaler")

This provides more accurate billing counts compared to the simple service instance count from the usage summary endpoint.