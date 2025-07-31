# CF Usage Service

A production-ready Go application that connects to the Cloud Foundry API to count billable Application Instances (AIs) and Service Instances (SIs) across organizations. The application identifies truly billable service instances by analyzing service plans and offerings, filtering out non-billable services like user-provided services.

Can run as a CLI tool for one-time reporting or as a web server exposing Prometheus metrics.

## Prerequisites

- Go 1.19 or later
- Cloud Foundry credentials (username/password)

## Building

```bash
go build -o tpcf-usage-service
```

## Authentication

The application uses environment variables for authentication and requires **no CF CLI installation**:

### Required Environment Variables

```bash
export CF_API_ENDPOINT="https://api.your-cf-domain.com"
export CF_USERNAME="your-username"
export CF_PASSWORD="your-password"
```

### Optional Environment Variables

```bash
# Skip SSL certificate verification (for development environments)
export CF_SKIP_SSL_VALIDATION=true

# Custom OAuth client credentials (if required by your CF environment)
export CF_CLIENT_ID="custom-client"
export CF_CLIENT_SECRET="custom-secret"
```

## Usage

### CLI Mode (One-time reporting)

```bash
# Basic usage (skips 'system' org by default)
CF_API_ENDPOINT=https://api.your-cf.com CF_USERNAME=admin CF_PASSWORD=secret ./tpcf-usage-service

# Skip multiple orgs
./tpcf-usage-service --skip-orgs "system,another-org"

# Verbose output
./tpcf-usage-service --verbose

# JSON output for automation
./tpcf-usage-service --json

# Skip SSL validation (for dev environments)
CF_SKIP_SSL_VALIDATION=true ./tpcf-usage-service
```

### Server Mode (Prometheus metrics)

```bash
# Start web server on default port 8080 (refreshes every hour)
CF_API_ENDPOINT=https://api.your-cf.com CF_USERNAME=admin CF_PASSWORD=secret ./tpcf-usage-service --server

# Start on custom port
./tpcf-usage-service --server --port 9090

# Custom refresh interval (refresh every 30 minutes)
./tpcf-usage-service --server --refresh-interval 30

# Server with verbose logging
./tpcf-usage-service --server --verbose
```

**Endpoints:**
- `GET /metrics` - Prometheus metrics endpoint (returns cached data)
- `GET /health` - Health check endpoint

**Server Behavior:**
- Fetches data from Cloud Foundry API on startup and then periodically based on `--refresh-interval`
- Metrics endpoint returns cached data (no API calls on each request)
- Background refresh continues until server shutdown

## Container Deployment

The application is container-ready with no external dependencies. See the example files:

- **Docker**: `Dockerfile` - Multi-stage build for minimal container image
- **Kubernetes**: `k8s-deployment.yaml` - Complete deployment with secrets, service, and ServiceMonitor for Prometheus

### Building the Container

```bash
docker build -t your-registry/tpcf-usage-service:latest .
```

### Kubernetes Deployment

```bash
# Update k8s-deployment.yaml with your CF endpoint and credentials
kubectl apply -f k8s-deployment.yaml
```

## Cloud Foundry Deployment

You can also deploy this application to Cloud Foundry itself. See the example file:

- **Cloud Foundry**: `manifest.yml` - CF deployment manifest with health checks and environment configuration

### Deploying to Cloud Foundry

```bash
# 1. Update manifest.yml with your CF domain and API endpoint
# 2. Set sensitive environment variables (don't put passwords in manifest!)
cf set-env tpcf-usage-service CF_USERNAME "admin"
cf set-env tpcf-usage-service CF_PASSWORD "your-password-here"

# 3. Optional: Set other environment variables
cf set-env tpcf-usage-service CF_SKIP_SSL_VALIDATION "false"

# 4. Deploy the application
cf push

# 5. Check the application is running
cf apps
cf logs tpcf-usage-service --recent
```

### CF Deployment Notes

- The app uses the Go buildpack and builds automatically during `cf push`
- Health checks are configured to use the `/health` endpoint
- The app will listen on the `$PORT` environment variable provided by CF
- Credentials are set via `cf set-env` to avoid storing passwords in the manifest
- The app can monitor the same CF instance it's deployed to, or a different one

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
2025/07/31 09:54:21 WARNING: SSL certificate verification is disabled
2025/07/31 09:54:21 Discovered OAuth token endpoint: https://login.sys.tas.example.com/oauth/token
2025/07/31 09:54:22 Successfully authenticated via direct OAuth
2025/07/31 09:54:22 Using environment variable credentials with direct API calls
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

## Authentication Flow

The application uses a pure OAuth 2.0 implementation:

1. **Endpoint Discovery**: Queries `/v2/info` to discover the OAuth authorization endpoint
2. **Direct OAuth Authentication**: Uses password grant flow with discovered endpoint
3. **Token Management**: Extracts and uses Bearer tokens for all API calls
4. **Fallback Logic**: Tries common endpoints if discovery fails
5. **No External Dependencies**: Pure Go HTTP client, no CF CLI required

## Production Features

- **Environment Variable Configuration**: All settings via env vars for containerization
- **SSL Flexibility**: Can skip SSL validation for development environments
- **Custom OAuth Clients**: Supports custom client credentials if required
- **Graceful Error Handling**: Clear error messages and proper HTTP status codes
- **Direct API Access**: High-performance HTTP calls without subprocess overhead
- **Container Ready**: Single binary with no external dependencies

## Billable Service Detection

The application determines which service instances are billable by:

1. Fetching all service plans from `/v3/service_plans`
2. Fetching all service offerings from `/v3/service_offerings`
3. For each service instance, looking up its service plan GUID
4. Using the plan's service offering GUID to identify the offering name
5. Filtering against known billable offerings:
   - `p.mysql` / `p-mysql`
   - `p.rabbitmq` / `p-rabbitmq` 
   - `p.redis` / `p-redis`
   - `postgres`
   - `genai` / `genai-service`

This provides more accurate billing counts compared to the simple service instance count from the usage summary endpoint.