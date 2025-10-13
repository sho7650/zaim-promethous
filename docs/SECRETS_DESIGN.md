# Docker Secrets Integration Design

## 問題分析

### 🔴 Critical Issue 1: Shell Expansion in docker-compose

**問題箇所**: [`docs/DOCKER.md:360`](docs/DOCKER.md#L360)

```yaml
environment:
  - REDIS_URL=redis://:$(cat /run/secrets/redis_password)@redis:6379/0
```

**問題点**:
- docker-compose は環境変数の値で **シェル展開を実行しない**
- 結果: 文字列 `"$(cat /run/secrets/redis_password)"` がそのまま渡される
- Redis 接続文字列が無効になり、アプリケーションが起動しない

**影響**: 🚨 Complete application failure - Redis connection impossible

---

### 🟡 Important Issue 2: Missing Import

**問題箇所**: [`docs/IMPLEMENTATION.md:1033`](docs/IMPLEMENTATION.md#L1033)

```go
func getSecretOrEnv(key, fallback string) string {
    secretPath := fmt.Sprintf("/run/secrets/%s", strings.ToLower(key))  // ← strings パッケージ使用
    if data, err := os.ReadFile(secretPath); err == nil {
        return strings.TrimSpace(string(data))  // ← strings パッケージ使用
    }
    // ...
}
```

**問題点**:
- `strings.ToLower()` と `strings.TrimSpace()` を使用
- しかし [`docs/IMPLEMENTATION.md:887`](docs/IMPLEMENTATION.md#L887) のインポートリストに `"strings"` が無い

**影響**: 🚨 Build failure - Compilation error

---

## 解決策設計

### ✅ Solution 1: Go-Based Configuration (Recommended)

Redis 接続設定を Go コード内で構築し、Docker secrets を自動的に読み込む。

#### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ docker-compose.yml                                          │
├─────────────────────────────────────────────────────────────┤
│ environment:                                                │
│   - REDIS_HOST=redis                                        │
│   - REDIS_PORT=6379                                         │
│   - REDIS_DB=0                                              │
│   # REDIS_PASSWORD は secrets から自動読み込み              │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ Go Application (cmd/exporter/main.go)                       │
├─────────────────────────────────────────────────────────────┤
│ loadConfig() {                                              │
│   password := getSecretOrEnv("REDIS_PASSWORD", "")         │
│     → Reads /run/secrets/redis_password                     │
│                                                             │
│   if REDIS_URL not set:                                     │
│     REDIS_URL = buildRedisURL(host, port, password, db)    │
│ }                                                           │
└─────────────────────────────────────────────────────────────┘
```

#### Code Implementation

**1. Enhanced Config Structure**

```go
// cmd/exporter/main.go

import (
    "context"
    "flag"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "strings"  // ← ADD THIS (Fix for Issue 2)
    "syscall"
    "time"

    "github.com/joho/godotenv"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/yourusername/zaim-prometheus-exporter/internal/metrics"
    "github.com/yourusername/zaim-prometheus-exporter/internal/server"
    "github.com/yourusername/zaim-prometheus-exporter/internal/storage"
    "github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
    "go.uber.org/zap"
)

type Config struct {
    ConsumerKey    string
    ConsumerSecret string
    CallbackURL    string
    TokenFile      string
    EncryptionKey  string

    // Redis configuration components
    RedisHost     string
    RedisPort     int
    RedisPassword string
    RedisDB       int
    RedisURL      string  // Constructed or explicitly provided

    Port int
}
```

**2. Enhanced loadConfig() Function**

```go
func loadConfig() *Config {
    cfg := &Config{
        ConsumerKey:    getEnv("ZAIM_CONSUMER_KEY", ""),
        ConsumerSecret: getEnv("ZAIM_CONSUMER_SECRET", ""),
        CallbackURL:    getEnv("ZAIM_CALLBACK_URL", "http://localhost:8080/zaim/auth/callback"),
        TokenFile:      getEnv("TOKEN_FILE", "/data/oauth_tokens.json"),
        EncryptionKey:  getSecretOrEnv("ENCRYPTION_KEY", ""),

        // Redis components (password auto-loaded from secrets)
        RedisHost:     getEnv("REDIS_HOST", "redis"),
        RedisPort:     getEnvInt("REDIS_PORT", 6379),
        RedisPassword: getSecretOrEnv("REDIS_PASSWORD", ""),
        RedisDB:       getEnvInt("REDIS_DB", 0),

        Port: getEnvInt("PORT", 8080),
    }

    // REDIS_URL priority:
    // 1. Explicit REDIS_URL environment variable (if provided)
    // 2. Constructed from components (host, port, password, db)
    if redisURL := getEnv("REDIS_URL", ""); redisURL != "" {
        cfg.RedisURL = redisURL
    } else {
        cfg.RedisURL = buildRedisURL(cfg.RedisHost, cfg.RedisPort, cfg.RedisPassword, cfg.RedisDB)
    }

    return cfg
}
```

**3. Redis URL Builder**

```go
// buildRedisURL constructs Redis connection string from components
func buildRedisURL(host string, port int, password string, db int) string {
    if password != "" {
        // With authentication
        return fmt.Sprintf("redis://:%s@%s:%d/%d", password, host, port, db)
    }
    // Without authentication (development only)
    return fmt.Sprintf("redis://%s:%d/%d", host, port, db)
}
```

**4. Keep Existing Helper Functions**

```go
// getSecretOrEnv: Docker Secrets (/run/secrets/) を優先、次に環境変数を確認
func getSecretOrEnv(key, fallback string) string {
    // Docker Secrets: /run/secrets/<key_lowercase>
    secretPath := fmt.Sprintf("/run/secrets/%s", strings.ToLower(key))
    if data, err := os.ReadFile(secretPath); err == nil {
        return strings.TrimSpace(string(data))
    }
    // Fallback to environment variable
    if value := os.Getenv(key); value != "" {
        return value
    }
    return fallback
}

func getEnv(key, fallback string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return fallback
}

func getEnvInt(key string, fallback int) int {
    if value := os.Getenv(key); value != "" {
        var i int
        fmt.Sscanf(value, "%d", &i)
        return i
    }
    return fallback
}
```

#### Updated docker-compose.yml

```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    secrets:
      - redis_password
    command: sh -c 'redis-server --requirepass "$$(cat /run/secrets/redis_password)"'
    volumes:
      - redis_data:/data
    networks:
      - internal
    restart: unless-stopped

  zaim-exporter:
    image: zaim-exporter:latest
    secrets:
      - encryption_key
      - redis_password
    environment:
      # Zaim API credentials
      - ZAIM_CONSUMER_KEY=${ZAIM_CONSUMER_KEY}
      - ZAIM_CONSUMER_SECRET=${ZAIM_CONSUMER_SECRET}
      - ZAIM_CALLBACK_URL=https://zaim.yourdomain.com/zaim/auth/callback

      # Redis components (REDIS_URL auto-constructed from these)
      - REDIS_HOST=redis
      - REDIS_PORT=6379
      - REDIS_DB=0
      # REDIS_PASSWORD automatically read from /run/secrets/redis_password

      # Secrets (auto-loaded by getSecretOrEnv)
      # - ENCRYPTION_KEY (from /run/secrets/encryption_key)
      # - REDIS_PASSWORD (from /run/secrets/redis_password)
    volumes:
      - ./data:/data
    networks:
      - traefik
      - internal
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.zaim.rule=Host(`zaim.yourdomain.com`)"
      - "traefik.http.routers.zaim.entrypoints=websecure"
      - "traefik.http.routers.zaim.tls.certresolver=letsencrypt"
      - "traefik.http.services.zaim.loadbalancer.server.port=8080"
    restart: unless-stopped
    depends_on:
      - redis

networks:
  traefik:
    external: true
  internal:
    driver: bridge

volumes:
  redis_data:

secrets:
  encryption_key:
    external: true
  redis_password:
    external: true
```

#### Benefits

✅ **No shell expansion needed** - All configuration in Go code
✅ **Automatic secret loading** - `getSecretOrEnv` handles `/run/secrets/`
✅ **Flexible configuration** - Can override via explicit `REDIS_URL` if needed
✅ **Component-based** - Easy to modify individual Redis settings
✅ **Secure** - No plaintext passwords in docker-compose.yml
✅ **Testable** - Easy to unit test configuration logic

---

### 🔧 Solution 2: Shell-Based Entrypoint (Alternative)

Docker entrypoint スクリプトで secrets を読み込み、環境変数を構築してからアプリケーションを起動する。

#### Implementation

**1. Create Entrypoint Script**

```bash
#!/bin/sh
# scripts/docker-entrypoint.sh
set -e

# Construct REDIS_URL from secrets if not explicitly provided
if [ -z "$REDIS_URL" ] && [ -f /run/secrets/redis_password ]; then
    REDIS_PASSWORD=$(cat /run/secrets/redis_password)
    REDIS_HOST=${REDIS_HOST:-redis}
    REDIS_PORT=${REDIS_PORT:-6379}
    REDIS_DB=${REDIS_DB:-0}

    export REDIS_URL="redis://:${REDIS_PASSWORD}@${REDIS_HOST}:${REDIS_PORT}/${REDIS_DB}"
    echo "✓ REDIS_URL constructed from secrets"
fi

# Construct ENCRYPTION_KEY path hint (optional, since Go code handles this)
if [ -f /run/secrets/encryption_key ]; then
    echo "✓ Encryption key secret available"
fi

# Execute main application with all arguments
exec "$@"
```

**2. Update Dockerfile**

```dockerfile
# Dockerfile

FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o zaim-exporter ./cmd/exporter

FROM alpine:latest
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/zaim-exporter .

# Add entrypoint script
COPY scripts/docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["./zaim-exporter"]
```

**3. Simplified docker-compose.yml**

```yaml
zaim-exporter:
  image: zaim-exporter:latest
  secrets:
    - encryption_key
    - redis_password
  environment:
    - ZAIM_CONSUMER_KEY=${ZAIM_CONSUMER_KEY}
    - ZAIM_CONSUMER_SECRET=${ZAIM_CONSUMER_SECRET}
    - ZAIM_CALLBACK_URL=https://zaim.yourdomain.com/zaim/auth/callback
    - REDIS_HOST=redis
    - REDIS_PORT=6379
    - REDIS_DB=0
    # REDIS_URL constructed by entrypoint.sh
  # ... rest of config
```

#### Benefits

✅ **Shell-based flexibility** - Complex environment construction
✅ **Pre-startup validation** - Can check secrets before app starts
✅ **Logging** - Can log configuration status
⚠️ **More complex** - Additional script to maintain
⚠️ **Shell dependency** - Requires shell in container

---

## Migration Path

### Phase 1: Fix Critical Issues (Immediate)

1. **Fix missing import** in `docs/IMPLEMENTATION.md:887`
   - Add `"strings"` to import list

2. **Remove invalid shell expansion** in `docs/DOCKER.md:360`
   - Replace with component-based configuration

### Phase 2: Implement Go-Based Solution (Recommended)

1. Update `cmd/exporter/main.go`:
   - Add `"strings"` import
   - Enhance `Config` struct with Redis components
   - Implement `buildRedisURL()` helper
   - Update `loadConfig()` with auto-construction logic

2. Update `docs/DOCKER.md`:
   - Replace shell expansion example with component-based config
   - Document the automatic URL construction behavior

3. Update `docs/IMPLEMENTATION.md`:
   - Add import fix
   - Document enhanced configuration system

### Phase 3: Document Alternative (Optional)

1. Create entrypoint script example
2. Document shell-based approach in `docs/DOCKER.md`
3. Explain when to use each approach

---

## Testing Checklist

### Unit Tests

```go
func TestBuildRedisURL(t *testing.T) {
    tests := []struct {
        name     string
        host     string
        port     int
        password string
        db       int
        want     string
    }{
        {
            name:     "with password",
            host:     "redis",
            port:     6379,
            password: "secret123",
            db:       0,
            want:     "redis://:secret123@redis:6379/0",
        },
        {
            name:     "without password",
            host:     "localhost",
            port:     6379,
            password: "",
            db:       1,
            want:     "redis://localhost:6379/1",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := buildRedisURL(tt.host, tt.port, tt.password, tt.db)
            if got != tt.want {
                t.Errorf("buildRedisURL() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Integration Tests

- [ ] Config loads with secrets present
- [ ] Config falls back to environment variables
- [ ] Redis connection succeeds with constructed URL
- [ ] Explicit REDIS_URL override works
- [ ] Missing secrets handled gracefully

### Deployment Tests

- [ ] docker-compose up succeeds
- [ ] Secrets properly mounted at `/run/secrets/`
- [ ] Application connects to Redis
- [ ] Logs show no secret-related errors

---

## Security Considerations

### ✅ Secure Practices

- Secrets stored in Docker secrets (encrypted at rest)
- No plaintext passwords in docker-compose.yml
- Secrets read at runtime only
- No secret logging (trim whitespace only)

### ⚠️ Avoid

- Shell expansion in docker-compose environment values
- Plaintext secrets in .env files committed to git
- Logging full connection strings with passwords
- Storing secrets in container images

---

## Comparison Matrix

| Aspect | Go-Based (Recommended) | Shell-Based (Alternative) |
|--------|------------------------|---------------------------|
| **Complexity** | Medium | High |
| **Maintainability** | High (Go code) | Medium (Shell script) |
| **Testability** | High (Unit tests) | Low (Integration only) |
| **Flexibility** | High (Easy override) | High (Shell power) |
| **Security** | High (No shell exposure) | High (Secrets in memory) |
| **Performance** | Fast (No shell exec) | Fast (One-time setup) |
| **Debugging** | Easy (Go debugger) | Medium (Shell logging) |
| **Docker Dependency** | None (Pure Go) | Shell in container |

**Recommendation**: Use **Go-Based** approach for production. Consider Shell-Based only for complex environment construction needs.

---

## References

- [Docker Secrets Documentation](https://docs.docker.com/engine/swarm/secrets/)
- [Redis Connection URL Format](https://www.iana.org/assignments/uri-schemes/prov/redis)
- [Go os.ReadFile Documentation](https://pkg.go.dev/os#ReadFile)
