# Zaim Prometheus Exporter - Scalability & Production Design

## 概要

このドキュメントは、単一インスタンス設計から将来的な水平スケーリングへの移行を見据えた設計改善を提案します。

## 懸念事項への対応

### 1. OAuth Request Token の共有ストレージ化

#### 現状の問題

```go
// internal/server/handlers.go (現状)
type Server struct {
    requestTokens map[string]*oauth.RequestToken  // ❌ プロセスメモリ内
    tokenMu       sync.Mutex
}
```

**問題点**:
- 複数インスタンス起動時、各プロセスが別々の map を持つ
- ロードバランサー経由で `/zaim/auth/login` と `/zaim/auth/callback` が別インスタンスに振り分けられると認証失敗
- スケールアウト時に破綻

#### 解決策 A: Redis 共有ストレージ (推奨)

**アーキテクチャ**:

```
┌─────────────────┐
│  Instance 1     │
│  /auth/login    │ ──┐
└─────────────────┘   │
                      ├─→ [Redis]
┌─────────────────┐   │    ├─ request_token:abc123 → {token, secret}
│  Instance 2     │   │    ├─ request_token:def456 → {token, secret}
│  /auth/callback │ ──┘    └─ TTL: 10 minutes
└─────────────────┘
```

**実装**:

##### `internal/storage/request_token_store.go` (新規)

```go
package storage

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/mrjones/oauth"
    "github.com/redis/go-redis/v9"
)

// RequestTokenStore は OAuth Request Token を保存
type RequestTokenStore interface {
    Save(ctx context.Context, token *oauth.RequestToken) error
    Load(ctx context.Context, tokenKey string) (*oauth.RequestToken, error)
    Delete(ctx context.Context, tokenKey string) error
}

// RedisRequestTokenStore は Redis ベースの実装
type RedisRequestTokenStore struct {
    client *redis.Client
    ttl    time.Duration
}

// NewRedisRequestTokenStore は RedisRequestTokenStore を作成
func NewRedisRequestTokenStore(redisURL string, ttl time.Duration) (*RedisRequestTokenStore, error) {
    opt, err := redis.ParseURL(redisURL)
    if err != nil {
        return nil, fmt.Errorf("failed to parse redis URL: %w", err)
    }

    client := redis.NewClient(opt)

    // 接続テスト
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := client.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("failed to connect to redis: %w", err)
    }

    return &RedisRequestTokenStore{
        client: client,
        ttl:    ttl,
    }, nil
}

// Save は Request Token を保存
func (s *RedisRequestTokenStore) Save(ctx context.Context, token *oauth.RequestToken) error {
    data, err := json.Marshal(token)
    if err != nil {
        return fmt.Errorf("failed to marshal token: %w", err)
    }

    key := fmt.Sprintf("request_token:%s", token.Token)
    if err := s.client.Set(ctx, key, data, s.ttl).Err(); err != nil {
        return fmt.Errorf("failed to save token to redis: %w", err)
    }

    return nil
}

// Load は Request Token を読み込み
func (s *RedisRequestTokenStore) Load(ctx context.Context, tokenKey string) (*oauth.RequestToken, error) {
    key := fmt.Sprintf("request_token:%s", tokenKey)
    data, err := s.client.Get(ctx, key).Bytes()
    if err != nil {
        if err == redis.Nil {
            return nil, fmt.Errorf("token not found or expired")
        }
        return nil, fmt.Errorf("failed to load token from redis: %w", err)
    }

    var token oauth.RequestToken
    if err := json.Unmarshal(data, &token); err != nil {
        return nil, fmt.Errorf("failed to unmarshal token: %w", err)
    }

    return &token, nil
}

// Delete は Request Token を削除
func (s *RedisRequestTokenStore) Delete(ctx context.Context, tokenKey string) error {
    key := fmt.Sprintf("request_token:%s", tokenKey)
    if err := s.client.Del(ctx, key).Err(); err != nil {
        return fmt.Errorf("failed to delete token from redis: %w", err)
    }
    return nil
}

// Close は Redis 接続を閉じる
func (s *RedisRequestTokenStore) Close() error {
    return s.client.Close()
}

// MemoryRequestTokenStore はインメモリ実装 (単一インスタンス用)
type MemoryRequestTokenStore struct {
    tokens map[string]*oauth.RequestToken
    mu     sync.RWMutex
}

// NewMemoryRequestTokenStore はインメモリストアを作成
func NewMemoryRequestTokenStore() *MemoryRequestTokenStore {
    return &MemoryRequestTokenStore{
        tokens: make(map[string]*oauth.RequestToken),
    }
}

// Save は Request Token を保存
func (s *MemoryRequestTokenStore) Save(ctx context.Context, token *oauth.RequestToken) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.tokens[token.Token] = token
    return nil
}

// Load は Request Token を読み込み
func (s *MemoryRequestTokenStore) Load(ctx context.Context, tokenKey string) (*oauth.RequestToken, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    token, ok := s.tokens[tokenKey]
    if !ok {
        return nil, fmt.Errorf("token not found")
    }

    return token, nil
}

// Delete は Request Token を削除
func (s *MemoryRequestTokenStore) Delete(ctx context.Context, tokenKey string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    delete(s.tokens, tokenKey)
    return nil
}
```

##### `internal/server/handlers.go` (修正)

```go
package server

import (
    "context"
    "encoding/json"
    "net/http"
    "time"

    "github.com/gorilla/mux"
    "github.com/yourusername/zaim-prometheus-exporter/internal/storage"
    "github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
    "go.uber.org/zap"
)

// Server は HTTP サーバー
type Server struct {
    router            *mux.Router
    oauthMgr          *zaim.OAuthManager
    requestTokenStore storage.RequestTokenStore  // ✅ インターフェース化
    logger            *zap.Logger
}

// NewServer は Server を作成
func NewServer(
    oauthMgr *zaim.OAuthManager,
    requestTokenStore storage.RequestTokenStore,
    logger *zap.Logger,
) *Server {
    s := &Server{
        router:            mux.NewRouter(),
        oauthMgr:          oauthMgr,
        requestTokenStore: requestTokenStore,
        logger:            logger,
    }

    s.setupRoutes()
    return s
}

// handleLogin は OAuth 認証開始
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    url, requestToken, err := s.oauthMgr.GetAuthorizationURL()
    if err != nil {
        s.logger.Error("failed to get authorization URL", zap.Error(err))
        http.Error(w, "Failed to start OAuth flow", http.StatusInternalServerError)
        return
    }

    // Redis に保存
    if err := s.requestTokenStore.Save(ctx, requestToken); err != nil {
        s.logger.Error("failed to save request token", zap.Error(err))
        http.Error(w, "Failed to save token", http.StatusInternalServerError)
        return
    }

    // Zaim の認証ページにリダイレクト
    http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleCallback は OAuth コールバック
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    oauthToken := r.URL.Query().Get("oauth_token")
    oauthVerifier := r.URL.Query().Get("oauth_verifier")

    if oauthToken == "" || oauthVerifier == "" {
        http.Error(w, "Missing oauth_token or oauth_verifier", http.StatusBadRequest)
        return
    }

    // Redis から取得
    requestToken, err := s.requestTokenStore.Load(ctx, oauthToken)
    if err != nil {
        s.logger.Error("failed to load request token", zap.Error(err))
        http.Error(w, "Invalid or expired oauth_token", http.StatusBadRequest)
        return
    }

    // Access Token 交換
    if err := s.oauthMgr.ExchangeToken(requestToken, oauthVerifier); err != nil {
        s.logger.Error("failed to exchange token", zap.Error(err))
        http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
        return
    }

    // 使い終わったら削除
    _ = s.requestTokenStore.Delete(ctx, oauthToken)

    s.logger.Info("OAuth authentication successful")

    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write([]byte(`
        <h1>認証成功</h1>
        <p>Zaim との連携が完了しました。</p>
        <p><a href="/zaim/auth/status">認証状態を確認</a></p>
    `))
}
```

##### `cmd/exporter/main.go` (修正)

```go
func main() {
    // ... (省略)

    // Request Token ストア初期化
    var requestTokenStore storage.RequestTokenStore
    if redisURL := config.RedisURL; redisURL != "" {
        // Redis 使用 (本番環境)
        store, err := storage.NewRedisRequestTokenStore(redisURL, 10*time.Minute)
        if err != nil {
            logger.Fatal("failed to initialize redis store", zap.Error(err))
        }
        defer store.Close()
        requestTokenStore = store
        logger.Info("using redis for request token storage")
    } else {
        // メモリ使用 (開発環境)
        requestTokenStore = storage.NewMemoryRequestTokenStore()
        logger.Warn("using in-memory request token storage (not suitable for multiple instances)")
    }

    // HTTP サーバー初期化
    srv := server.NewServer(oauthMgr, requestTokenStore, logger)

    // ... (省略)
}

type Config struct {
    // ... (既存フィールド)
    RedisURL string  // 追加
}

func loadConfig() *Config {
    return &Config{
        // ... (既存設定)
        RedisURL: getEnv("REDIS_URL", ""),  // 追加
    }
}
```

##### `docker-compose.yml` (Redis 追加)

```yaml
services:
  # ========================================
  # Redis (Request Token 共有ストレージ)
  # ========================================
  redis:
    image: redis:7-alpine
    container_name: zaim-redis
    restart: unless-stopped

    command: redis-server --requirepass ${REDIS_PASSWORD}

    volumes:
      - redis-data:/data

    networks:
      - internal

    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 3s
      retries: 3

  # ========================================
  # Zaim Exporter (スケールアウト対応)
  # ========================================
  zaim-exporter:
    image: zaim-exporter:latest
    # replicas でスケールアウト可能
    deploy:
      replicas: 2  # 2インスタンス

    environment:
      - REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379/0
      # ... (既存環境変数)

    depends_on:
      redis:
        condition: service_healthy

volumes:
  redis-data:
```

#### 解決策 B: Sticky Session (簡易的)

Redis を使わない場合の代替案。

##### `docker-compose.yml` (Traefik ラベル追加)

```yaml
services:
  zaim-exporter:
    labels:
      # Sticky Session 有効化
      - "traefik.http.services.zaim-auth-service.loadbalancer.sticky.cookie=true"
      - "traefik.http.services.zaim-auth-service.loadbalancer.sticky.cookie.name=zaim_session"
      - "traefik.http.services.zaim-auth-service.loadbalancer.sticky.cookie.secure=true"
      - "traefik.http.services.zaim-auth-service.loadbalancer.sticky.cookie.httpOnly=true"
```

**制限事項**:
- インスタンスがダウンすると認証中のセッションが失われる
- Cookie が無効化されているブラウザでは動作しない
- 本番環境では Redis の使用を推奨

---

### 2. 暗号化キーの運用管理

#### 現状の問題

- `.env` に平文で保存
- キー生成手順が不明確
- ローテーション手順が存在しない
- 鍵の保管場所が運用担当者に委ねられている

#### 解決策: 暗号化キー管理の標準化

##### キー生成手順

```bash
# 1. 新しい暗号化キー生成
openssl rand -base64 32

# 出力例:
# YjNhZjBkMmU5Nzg1YmM0ZTYxZjIzNDU2Nzg5MGFiY2Q=

# 2. .env に保存 (開発環境のみ)
echo "ENCRYPTION_KEY=YjNhZjBkMmU5Nzg1YmM0ZTYxZjIzNDU2Nzg5MGFiY2Q=" >> .env

# 3. 本番環境では Docker Secrets または外部 KMS を使用 (後述)
```

##### キーローテーション手順

```bash
#!/bin/bash
# /usr/local/bin/rotate-encryption-key.sh

set -euo pipefail

OLD_KEY="${ENCRYPTION_KEY}"
NEW_KEY=$(openssl rand -base64 32)

echo "🔑 Generating new encryption key..."
echo "Old key: ${OLD_KEY:0:10}..."
echo "New key: ${NEW_KEY:0:10}..."

# 1. 既存トークンを復号化
echo "📂 Decrypting existing token..."
docker exec zaim-exporter /zaim-exporter decrypt-token \
    --input /data/oauth_tokens.json \
    --output /tmp/tokens-plaintext.json \
    --key "$OLD_KEY"

# 2. 新しいキーで再暗号化
echo "🔒 Re-encrypting with new key..."
docker exec zaim-exporter /zaim-exporter encrypt-token \
    --input /tmp/tokens-plaintext.json \
    --output /data/oauth_tokens.json \
    --key "$NEW_KEY"

# 3. 環境変数更新
echo "💾 Updating environment..."
sed -i.bak "s/ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$NEW_KEY/" .env

# 4. コンテナ再起動
echo "🔄 Restarting container..."
docker-compose restart zaim-exporter

# 5. 動作確認
echo "✅ Verifying..."
sleep 5
curl -f http://localhost:8080/health || {
    echo "❌ Health check failed! Rolling back..."
    mv .env.bak .env
    docker-compose restart zaim-exporter
    exit 1
}

echo "✅ Encryption key rotated successfully"
rm .env.bak
```

##### 本番環境: Docker Secrets の使用 (推奨)

```yaml
# docker-compose.yml (本番環境)
version: '3.9'

services:
  zaim-exporter:
    secrets:
      - encryption_key
    # 環境変数不要: getSecretOrEnv() が自動的に /run/secrets/encryption_key を読み込む

secrets:
  encryption_key:
    external: true  # docker secret create で作成
```

```bash
# Docker Secret 作成
openssl rand -base64 32 | docker secret create encryption_key -

# Secret の確認 (値は見えない)
docker secret ls

# Secret のローテーション
NEW_KEY=$(openssl rand -base64 32)
echo "$NEW_KEY" | docker secret create encryption_key_v2 -

# docker-compose.yml で encryption_key → encryption_key_v2 に変更
docker stack deploy -c docker-compose.yml zaim

# 古い Secret 削除
docker secret rm encryption_key
```

**注意**: アプリケーションは `/run/secrets/encryption_key` を自動的に読み込みます (IMPLEMENTATION.md の getSecretOrEnv 関数参照)。環境変数 `ENCRYPTION_KEY` の設定は不要です。

---

### 3. IP ホワイトリスト監視・検証

#### 現状の問題

- CIDR 設定ミスで外部公開される可能性
- ネットワーク変更時の検証手順がない
- アクセスログの監視方法が不明確

#### 解決策 A: 自動テストスクリプト

##### `/usr/local/bin/test-ip-whitelist.sh`

```bash
#!/bin/bash
# IP ホワイトリストの動作確認

set -euo pipefail

METRICS_URL="https://zaim.yourdomain.com/metrics"
EXPECTED_INTERNAL_IP="192.168.1.100"  # Prometheus サーバーの IP

echo "🔍 Testing IP whitelist configuration..."

# Test 1: 外部 IP からのアクセスは拒否されるべき
echo "Test 1: External access should be blocked"
if curl -sS -o /dev/null -w "%{http_code}" "$METRICS_URL" | grep -q "403\|404"; then
    echo "✅ External access blocked (expected)"
else
    echo "❌ CRITICAL: External access allowed! Metrics endpoint is publicly accessible!"
    exit 1
fi

# Test 2: 内部 IP からのアクセスは許可されるべき
echo "Test 2: Internal access should be allowed"
if ssh "$EXPECTED_INTERNAL_IP" "curl -sS -o /dev/null -w '%{http_code}' '$METRICS_URL'" | grep -q "200"; then
    echo "✅ Internal access allowed (expected)"
else
    echo "❌ WARNING: Internal access blocked! Check CIDR configuration"
    exit 1
fi

echo "✅ All IP whitelist tests passed"
```

```bash
# cron で定期実行
0 */6 * * * /usr/local/bin/test-ip-whitelist.sh >> /var/log/ip-whitelist-test.log 2>&1
```

#### 解決策 B: Traefik アクセスログ監視

##### `docker-compose.yml` (Traefik ログ有効化)

```yaml
services:
  traefik:
    command:
      # ... (既存設定)
      - "--accesslog=true"
      - "--accesslog.filepath=/var/log/traefik/access.log"
      - "--accesslog.format=json"
      - "--accesslog.filters.statuscodes=200,403,404"

    volumes:
      - traefik-logs:/var/log/traefik
```

##### アクセスログ監視スクリプト

```bash
#!/bin/bash
# /usr/local/bin/monitor-metrics-access.sh

LOG_FILE="/var/log/traefik/access.log"
ALERT_EMAIL="ops@example.com"

# /metrics へのアクセスログを抽出
jq -r 'select(.RequestPath == "/metrics") |
    "\(.time) \(.ClientAddr) \(.RequestPath) \(.DownstreamStatus)"' \
    "$LOG_FILE" | while read -r line; do

    IP=$(echo "$line" | awk '{print $2}' | cut -d: -f1)
    STATUS=$(echo "$line" | awk '{print $4}')

    # 200 OK かつ内部 IP 以外の場合アラート
    if [[ "$STATUS" == "200" ]] && ! echo "$IP" | grep -qE "^(192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|127\.0\.0\.1)"; then
        echo "⚠️ ALERT: Metrics accessed from external IP: $line"
        echo "$line" | mail -s "[ALERT] Metrics accessed from external IP" "$ALERT_EMAIL"
    fi
done
```

#### 解決策 C: Prometheus の Blackbox Exporter で定期監視

```yaml
# prometheus.yml
scrape_configs:
  # Metrics エンドポイントの外部アクセス監視
  - job_name: 'blackbox-zaim-metrics-external'
    metrics_path: /probe
    params:
      module: [http_2xx]
    static_configs:
      - targets:
          - https://zaim.yourdomain.com/metrics
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: blackbox-exporter:9115

  # アラート設定
  alerting:
    alertmanagers:
      - static_configs:
          - targets: ['alertmanager:9093']
```

```yaml
# alerts/zaim.yml
groups:
  - name: zaim_security
    rules:
      # 外部から /metrics にアクセス可能な場合アラート
      - alert: MetricsPubliclyAccessible
        expr: probe_success{job="blackbox-zaim-metrics-external"} == 1
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Zaim metrics endpoint is publicly accessible"
          description: "The /metrics endpoint is responding to external requests. Check IP whitelist configuration immediately."
```

---

### 4. Import パスの修正

#### 問題

```go
// 現状 (プレースホルダ)
import "github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
```

#### 解決策: プロジェクト初期化スクリプト

##### `/scripts/init-project.sh`

```bash
#!/bin/bash
# プロジェクト初期化スクリプト

set -euo pipefail

echo "🚀 Zaim Prometheus Exporter - Project Initialization"
echo ""

# ユーザー情報取得
read -p "GitHub username or organization: " GITHUB_USER
read -p "Repository name [zaim-prometheus-exporter]: " REPO_NAME
REPO_NAME=${REPO_NAME:-zaim-prometheus-exporter}

MODULE_PATH="github.com/$GITHUB_USER/$REPO_NAME"

echo ""
echo "Module path will be: $MODULE_PATH"
read -p "Proceed? (y/N): " CONFIRM

if [[ "$CONFIRM" != "y" ]]; then
    echo "Aborted."
    exit 1
fi

# go.mod 初期化
echo "📦 Initializing go.mod..."
go mod init "$MODULE_PATH"

# import パス置換
echo "🔧 Updating import paths..."
find . -type f -name "*.go" -exec sed -i.bak \
    "s|github.com/yourusername/zaim-prometheus-exporter|$MODULE_PATH|g" {} +

# バックアップ削除
find . -name "*.go.bak" -delete

# 依存関係インストール
echo "📥 Installing dependencies..."
go get github.com/mrjones/oauth@latest
go get github.com/prometheus/client_golang@latest
go get github.com/gorilla/mux@latest
go get github.com/joho/godotenv@latest
go get go.uber.org/zap@latest
go get github.com/redis/go-redis/v9@latest

go mod tidy

echo ""
echo "✅ Project initialized successfully!"
echo "Module path: $MODULE_PATH"
echo ""
echo "Next steps:"
echo "1. cp .env.example .env"
echo "2. Edit .env with your Zaim credentials"
echo "3. go build -o bin/zaim-exporter ./cmd/exporter"
echo "4. ./bin/zaim-exporter"
```

使用方法:

```bash
# 実行権限付与
chmod +x scripts/init-project.sh

# 実行
./scripts/init-project.sh

# 例:
# GitHub username: yourusername
# Repository name: zaim-prometheus-exporter
# → Module path: github.com/yourusername/zaim-prometheus-exporter
```

---

## 推奨デプロイメント構成

### 開発環境

```yaml
# docker-compose.dev.yml
services:
  zaim-exporter:
    build: .
    environment:
      - REDIS_URL=  # 空 (メモリ使用)
      - ENCRYPTION_KEY=  # 空 (平文保存)
    volumes:
      - ./data:/data
```

### ステージング環境

```yaml
# docker-compose.staging.yml
services:
  redis:
    image: redis:7-alpine

  zaim-exporter:
    deploy:
      replicas: 2
    environment:
      - REDIS_URL=redis://:password@redis:6379/0
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}  # .env から
```

### 本番環境

```yaml
# docker-compose.prod.yml
services:
  redis:
    image: redis:7-alpine
    command: redis-server --requirepass ${REDIS_PASSWORD}
    volumes:
      - redis-data:/data
    deploy:
      resources:
        limits:
          memory: 256M

  zaim-exporter:
    deploy:
      replicas: 3
      resources:
        limits:
          cpus: '0.5'
          memory: 128M
    secrets:
      - encryption_key
    environment:
      - REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379/0
      # ENCRYPTION_KEY は /run/secrets/encryption_key から自動読み込み (getSecretOrEnv 関数)
```

---

## 運用チェックリスト

### 初期セットアップ

- [ ] `scripts/init-project.sh` でプロジェクト初期化
- [ ] `.env` ファイル作成と環境変数設定
- [ ] 暗号化キー生成 (`openssl rand -base64 32`)
- [ ] Docker Secrets 設定 (本番環境)
- [ ] Traefik ネットワーク作成
- [ ] Zaim アプリケーション登録

### セキュリティ検証

- [ ] IP ホワイトリストテスト実行
- [ ] 外部からの /metrics アクセス確認 (403 であること)
- [ ] 内部からの /metrics アクセス確認 (200 であること)
- [ ] Traefik アクセスログ監視設定
- [ ] Prometheus Blackbox Exporter 設定

### スケーラビリティ検証

- [ ] Redis 接続確認
- [ ] 複数インスタンス起動テスト
- [ ] OAuth フロー動作確認 (複数インスタンス環境)
- [ ] ロードバランシング動作確認

### 定期運用タスク

- [ ] 暗号化キーローテーション (四半期ごと)
- [ ] OAuth トークンバックアップ (毎日)
- [ ] IP ホワイトリスト検証 (毎週)
- [ ] アクセスログ監視 (毎日)
- [ ] Redis データバックアップ (毎日)

---

## 次のステップ

1. **OPERATIONS.md** で日常運用手順を詳細化
2. **MONITORING.md** で監視・アラート設定を標準化
3. **TROUBLESHOOTING.md** でよくある問題と解決方法をドキュメント化
