# Zaim Prometheus Exporter

Prometheus exporter for [Zaim](https://zaim.net/) (Japanese household accounting service) that exposes financial metrics for monitoring and analysis.

## Features

- OAuth 1.0a authentication with Zaim API
- Prometheus metrics for payments and income
- Redis-based token storage for multi-instance deployment
- Docker Secrets support for secure configuration
- Encrypted token storage with AES-256-GCM
- Hourly and daily aggregation of transactions
- Health and readiness checks
- Web UI for authentication management

## Prerequisites

- Docker and Docker Compose (for containerized deployment)
- Go 1.21+ (for local development)
- Zaim API credentials (Consumer Key and Consumer Secret)

## Quick Start

### 1. Get Zaim API Credentials

1. Log in to [Zaim Developers](https://dev.zaim.net/)
2. Create a new application
3. Note your Consumer Key and Consumer Secret
4. Set Callback URL to `http://localhost:8080/zaim/auth/callback` (for local development)

### 2. Configure Environment

```bash
# Copy example environment file
cp .env.example .env

# Edit .env with your credentials
vim .env
# Set ZAIM_CONSUMER_KEY and ZAIM_CONSUMER_SECRET
```

### 3. Create Docker Secrets

```bash
# Create secrets directory
mkdir -p secrets

# Generate random passwords
openssl rand -base64 32 > secrets/redis_password.txt
openssl rand -base64 32 > secrets/encryption_key.txt
```

### 4. Start Services

```bash
# Build and start services
docker-compose up -d

# Check logs
docker-compose logs -f zaim-exporter
```

### 5. Authenticate with Zaim

1. Open http://localhost:8080 in your browser
2. Click "Authenticate with Zaim"
3. Log in to Zaim and authorize the application
4. You'll be redirected back with success message

### 6. Access Metrics

Metrics are available at http://localhost:8080/metrics

Prometheus is available at http://localhost:9090 (if enabled in docker-compose.yml)

## Available Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `zaim_payment_amount` | gauge | Total payment amount per hour | `hour` |
| `zaim_payment_count` | gauge | Number of payments per hour | `hour` |
| `zaim_income_amount` | gauge | Total income amount per hour | `hour` |
| `zaim_income_count` | gauge | Number of income transactions per hour | `hour` |
| `zaim_today_total_amount` | gauge | Today's total spending | - |
| `zaim_last_update` | gauge | Unix timestamp of last successful update | - |

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ZAIM_CONSUMER_KEY` | Zaim OAuth Consumer Key | Required |
| `ZAIM_CONSUMER_SECRET` | Zaim OAuth Consumer Secret | Required |
| `ZAIM_CALLBACK_URL` | OAuth callback URL | `http://localhost:8080/zaim/auth/callback` |
| `TOKEN_FILE` | Path to OAuth token storage | `/data/oauth_tokens.json` |
| `REDIS_HOST` | Redis hostname | `redis` |
| `REDIS_PORT` | Redis port | `6379` |
| `REDIS_DB` | Redis database number | `0` |
| `PORT` | HTTP server port | `8080` |

### Docker Secrets

The application supports reading sensitive configuration from Docker Secrets:
- `/run/secrets/encryption_key` - AES-256 encryption key for token storage
- `/run/secrets/redis_password` - Redis authentication password

## Development

### Local Build

```bash
# Install dependencies
go mod download

# Build
go build -o zaim-exporter ./cmd/exporter

# Run
./zaim-exporter
```

### Running Tests

```bash
# Unit tests
go test ./...

# With coverage
go test -cover ./...
```

### Docker Build

```bash
# Build image
docker build -t zaim-exporter:latest .

# Run container
docker run -p 8080:8080 zaim-exporter:latest
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Web UI dashboard |
| `/metrics` | GET | Prometheus metrics |
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check |
| `/zaim/auth/status` | GET | Authentication status |
| `/zaim/auth/start` | GET | Start OAuth flow |
| `/zaim/auth/callback` | GET | OAuth callback |
| `/zaim/auth/reset` | POST | Reset authentication |

## Production Deployment

### Using Traefik

The docker-compose.yml includes labels for Traefik integration:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.zaim-exporter.rule=Host(`zaim.yourdomain.com`)"
  - "traefik.http.routers.zaim-exporter.entrypoints=websecure"
  - "traefik.http.routers.zaim-exporter.tls.certresolver=letsencrypt"
```

### Scaling

For multi-instance deployment, ensure Redis is configured:

```yaml
deploy:
  replicas: 3  # Run 3 instances
```

Redis is used to share OAuth request tokens between instances during the authentication flow.

## Monitoring

### Grafana Dashboard

You can import the example dashboard from `grafana/dashboard.json` or create your own using the available metrics.

### Alerting Rules

Example Prometheus alerting rules:

```yaml
groups:
  - name: zaim
    rules:
      - alert: HighDailySpending
        expr: zaim_today_total_amount > 10000
        for: 5m
        annotations:
          summary: "High daily spending detected: {{ $value }} yen"
```

## Security Considerations

- OAuth tokens are encrypted using AES-256-GCM before storage
- Docker Secrets are used for sensitive configuration
- Redis requires password authentication
- HTTPS is recommended for production (use Traefik or similar)

## Troubleshooting

### Authentication Issues
```bash
# Check OAuth status
curl http://localhost:8080/zaim/auth/status

# Reset authentication
curl -X POST http://localhost:8080/zaim/auth/reset
```

### Redis Connection
```bash
# Check Redis connectivity
docker-compose exec redis redis-cli ping

# Check Redis with auth
docker-compose exec redis sh -c 'redis-cli -a $(cat /run/secrets/redis_password) ping'
```

### View Logs
```bash
# Application logs
docker-compose logs zaim-exporter

# Redis logs
docker-compose logs redis

# All services
docker-compose logs -f
```

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Support

For issues and questions, please use the GitHub issue tracker.