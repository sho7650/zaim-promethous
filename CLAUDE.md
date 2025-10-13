# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Google Apps Script project that serves as a Prometheus exporter for Zaim (家計簿サービス) data. The project fetches financial data from the Zaim API and exposes it in Prometheus metrics format. The codebase is transitioning from a Google Apps Script implementation to a containerized Go application.

## Architecture & Key Components

### Current Implementation (Google Apps Script)
- **Main file**: `コード.js` - Contains all the business logic
- **Configuration**: `appsscript.json` - Google Apps Script configuration
- **Deployment**: Uses clasp (`.clasp.json`) for deployment to Google Apps Script

### Key Functions in コード.js
- `doGet(e)` - Main entry point, handles HTTP requests and routes to appropriate handlers
- `getZaimService()` - Creates OAuth1 service for Zaim API authentication
- `serveMetrics(e)` - Serves Prometheus metrics (with optional token authentication)
- `getZaimData()` - Fetches financial data from Zaim API
- `aggregateByHour(zaimData)` / `aggregateByDay(zaimData)` - Aggregates financial data
- `generatePrometheusMetrics()` - Converts aggregated data to Prometheus format
- OAuth flow handlers: `showAuthPage()`, `handleOAuthCallback()`, `authCallback()`

### Planned Architecture (Go Application)
Documentation in `docs/` describes the planned migration:
- **ARCHITECTURE.md** - Complete system design for Go/Docker implementation
- **IMPLEMENTATION.md** - Step-by-step implementation guide
- **DOCKER.md** - Docker and Traefik integration details
- **SCALABILITY.md** - Future scaling considerations
- **SECRETS_DESIGN.md** - Security and secrets management
- **CRITICAL_FIXES.md** - Known issues and fixes

## Development Commands

### Google Apps Script Development
```bash
# Deploy to Google Apps Script
clasp push

# Open in Apps Script editor
clasp open

# View logs
clasp logs

# Run a function
clasp run <functionName>
```

### Testing
```bash
# Test OAuth flow locally
clasp run testZaimAPI

# Debug configuration
clasp run debugGetConfig
```

## Configuration & Authentication

### OAuth Configuration
The system uses OAuth 1.0a for Zaim API authentication:
1. Consumer Key and Secret stored in Script Properties
2. Access tokens stored in User Properties
3. Callback URL: Script URL with `authCallback` function

### Script Properties Required
- `ZAIM_CONSUMER_KEY` - Zaim OAuth consumer key
- `ZAIM_CONSUMER_SECRET` - Zaim OAuth consumer secret
- `API_TOKEN` (optional) - Token for metrics endpoint authentication

### API Endpoints
- `?mode=auth` - Start OAuth authentication flow
- `?mode=reset` - Reset OAuth authentication
- `?mode=metrics&token=<API_TOKEN>` - Fetch Prometheus metrics (default)
- OAuth callback handled automatically via `oauth_token` and `oauth_verifier` parameters

## Data Flow

1. **Metrics Request** → `doGet()` → `serveMetrics()`
2. **Cache Check** → 5-minute cache for Zaim API responses
3. **Zaim API Call** → `getZaimData()` fetches current month's transactions
4. **Data Aggregation** → Hourly and daily aggregation of transactions
5. **Metrics Generation** → Convert to Prometheus format with labels
6. **Response** → Text/plain response with Prometheus metrics

## Prometheus Metrics Format

The exporter generates the following metrics:
- `zaim_payment_amount{hour="YYYY-MM-DD HH:00:00"}` - Hourly payment amounts
- `zaim_payment_count{hour="YYYY-MM-DD HH:00:00"}` - Hourly payment counts
- `zaim_income_amount{hour="YYYY-MM-DD HH:00:00"}` - Hourly income amounts
- `zaim_today_total_amount` - Today's total spending
- `zaim_error{type="<error_type>"}` - Error indicators

## Important Implementation Notes

1. **Japanese Comments**: The main code file uses Japanese comments extensively. Key terms:
   - 支出 (shishutsu) = expenses/payments
   - 収入 (shūnyū) = income
   - 振替 (furikae) = transfers

2. **Time Handling**: 
   - All timestamps use JST (Asia/Tokyo timezone)
   - Aggregation is done by hour and day based on the `date` field

3. **Caching Strategy**:
   - 5-minute cache for API responses to avoid rate limits
   - Cache key: `zaim_metrics`

4. **Error Handling**:
   - Comprehensive logging with emoji indicators (✅, ❌, ⚠️, 📊, etc.)
   - Returns error metrics in Prometheus format on failures

5. **Migration Context**:
   - Project is transitioning from Google Apps Script to Go/Docker
   - Documentation in `docs/` folder describes the target architecture
   - Current implementation remains functional during migration

## Security Considerations

- OAuth tokens stored securely in Google's Properties Service
- Optional API token authentication for metrics endpoint
- Detailed logging for debugging (be careful with sensitive data)
- Consumer secrets should never be committed to version control