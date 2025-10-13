# Zaim Prometheus Exporter - Implementation Guide (Production-Ready)

## 前提条件

### 必須環境

- **Go**: 1.25+ (generics, slices パッケージなど最新機能を活用)
- **Docker**: 24.0+
- **Docker Compose**: 2.20+
- **Git**: バージョン管理
- **Redis**: 7.0+ (スケールアウト時)

### 推奨環境

- **OS**: Linux (Ubuntu 22.04+) または macOS
- **エディタ**: VS Code + Go 拡張機能
- **Traefik**: 既存環境で動作中

## プロジェクト初期化

### 1. リポジトリ作成と初期化スクリプト実行

```bash
cd /Users/sho/working/nodejs/work/zaim-premetheus

# 初期化スクリプト作成
mkdir -p scripts
cat > scripts/init-project.sh <<'EOF'
#!/bin/bash
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

# ディレクトリ構造作成
echo "📁 Creating directory structure..."
mkdir -p cmd/exporter
mkdir -p internal/{zaim,metrics,storage,server,config}
mkdir -p configs

# go.mod 初期化
echo "📦 Initializing go.mod..."
go mod init "$MODULE_PATH"

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
EOF

chmod +x scripts/init-project.sh

# 実行
./scripts/init-project.sh
```

### 2. go.mod 最終形態

```go
module github.com/yourusername/zaim-prometheus-exporter

go 1.25

require (
    github.com/gorilla/mux v1.8.1
    github.com/joho/godotenv v1.5.1
    github.com/mrjones/oauth v0.0.0-20190623134757-126b35219450
    github.com/prometheus/client_golang v1.18.0
    github.com/redis/go-redis/v9 v9.4.0
    go.uber.org/zap v1.26.0
)
```

## 実装ステップ

### Phase 1: データモデル定義 (30分)

#### `internal/zaim/models.go`

```go
package zaim

import "time"

// Zaim API /v2/home/money レスポンス
type MoneyResponse struct {
    Money []MoneyEntry `json:"money"`
}

type MoneyEntry struct {
    ID            int64  `json:"id"`
    Mode          string `json:"mode"` // "payment", "income", "transfer"
    Amount        int    `json:"amount"`
    Date          string `json:"date"`    // "2025-01-15"
    Created       string `json:"created"` // "2025-01-15 14:30:00"
    CategoryID    int    `json:"category_id"`
    GenreID       int    `json:"genre_id"`
    FromAccountID int    `json:"from_account_id"`
    ToAccountID   int    `json:"to_account_id,omitempty"`
    Comment       string `json:"comment,omitempty"`
    Name          string `json:"name"`
    Place         string `json:"place,omitempty"`
}

// CreatedTime は created フィールドを time.Time に変換
func (m *MoneyEntry) CreatedTime() (time.Time, error) {
    const layout = "2006-01-02 15:04:05"
    loc, _ := time.LoadLocation("Asia/Tokyo")
    return time.ParseInLocation(layout, m.Created, loc)
}

// IsToday は今日のデータかチェック
func (m *MoneyEntry) IsToday(now time.Time) bool {
    created, err := m.CreatedTime()
    if err != nil {
        return false
    }
    return created.Format("2006-01-02") == now.Format("2006-01-02")
}
```

### Phase 2: トークンストア実装 (1時間)

#### `internal/storage/token_store.go` (Access Token 用)

```go
package storage

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "sync"
)

// OAuthToken は Zaim OAuth トークンを保存
type OAuthToken struct {
    AccessToken       string `json:"access_token"`
    AccessTokenSecret string `json:"access_token_secret"`
    ConsumerKey       string `json:"consumer_key"`
    CreatedAt         string `json:"created_at"`
}

// TokenStore はトークンを永続化
type TokenStore struct {
    filePath      string
    encryptionKey []byte // nil の場合は平文保存
    mu            sync.RWMutex
}

// NewTokenStore は TokenStore を作成
func NewTokenStore(filePath string, encryptionKey string) *TokenStore {
    var key []byte
    if encryptionKey != "" {
        decoded, err := base64.StdEncoding.DecodeString(encryptionKey)
        if err == nil && len(decoded) == 32 {
            key = decoded
        }
    }

    return &TokenStore{
        filePath:      filePath,
        encryptionKey: key,
    }
}

// Save はトークンを保存
func (s *TokenStore) Save(token *OAuthToken) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    data, err := json.Marshal(token)
    if err != nil {
        return fmt.Errorf("failed to marshal token: %w", err)
    }

    if s.encryptionKey != nil {
        data, err = s.encrypt(data)
        if err != nil {
            return fmt.Errorf("failed to encrypt token: %w", err)
        }
    }

    if err := os.WriteFile(s.filePath, data, 0600); err != nil {
        return fmt.Errorf("failed to write token file: %w", err)
    }

    return nil
}

// Load はトークンを読み込み
func (s *TokenStore) Load() (*OAuthToken, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    data, err := os.ReadFile(s.filePath)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, fmt.Errorf("token file not found: %w", err)
        }
        return nil, fmt.Errorf("failed to read token file: %w", err)
    }

    if s.encryptionKey != nil {
        data, err = s.decrypt(data)
        if err != nil {
            return nil, fmt.Errorf("failed to decrypt token: %w", err)
        }
    }

    var token OAuthToken
    if err := json.Unmarshal(data, &token); err != nil {
        return nil, fmt.Errorf("failed to unmarshal token: %w", err)
    }

    return &token, nil
}

// Exists はトークンファイルが存在するかチェック
func (s *TokenStore) Exists() bool {
    _, err := os.Stat(s.filePath)
    return err == nil
}

// encrypt は AES-256-GCM で暗号化
func (s *TokenStore) encrypt(plaintext []byte) ([]byte, error) {
    block, err := aes.NewCipher(s.encryptionKey)
    if err != nil {
        return nil, err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return nil, err
    }

    ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
    return ciphertext, nil
}

// decrypt は AES-256-GCM で復号化
func (s *TokenStore) decrypt(ciphertext []byte) ([]byte, error) {
    block, err := aes.NewCipher(s.encryptionKey)
    if err != nil {
        return nil, err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonceSize := gcm.NonceSize()
    if len(ciphertext) < nonceSize {
        return nil, fmt.Errorf("ciphertext too short")
    }

    nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
    plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return nil, err
    }

    return plaintext, nil
}
```

#### `internal/storage/request_token_store.go` (Request Token 用、スケールアウト対応)

```go
package storage

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"

    "github.com/mrjones/oauth"
    "github.com/redis/go-redis/v9"
)

// RequestTokenStore は OAuth Request Token を保存するインターフェース
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

// MemoryRequestTokenStore はインメモリ実装 (開発環境用)
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

### Phase 3: Zaim OAuth クライアント実装 (1時間)

#### `internal/zaim/oauth.go`

```go
package zaim

import (
    "fmt"
    "time"

    "github.com/mrjones/oauth"
    "github.com/yourusername/zaim-prometheus-exporter/internal/storage"
)

const (
    RequestTokenURL = "https://api.zaim.net/v2/auth/request"
    AuthorizeURL    = "https://auth.zaim.net/users/auth"
    AccessTokenURL  = "https://api.zaim.net/v2/auth/access"
)

// OAuthManager は Zaim OAuth フローを管理
type OAuthManager struct {
    consumer   *oauth.Consumer
    tokenStore *storage.TokenStore
}

// NewOAuthManager は OAuthManager を作成
func NewOAuthManager(consumerKey, consumerSecret, callbackURL string, store *storage.TokenStore) *OAuthManager {
    consumer := oauth.NewConsumer(
        consumerKey,
        consumerSecret,
        oauth.ServiceProvider{
            RequestTokenUrl:   RequestTokenURL,
            AuthorizeTokenUrl: AuthorizeURL,
            AccessTokenUrl:    AccessTokenURL,
        },
    )

    consumer.AdditionalParams = map[string]string{
        "oauth_callback": callbackURL,
    }

    return &OAuthManager{
        consumer:   consumer,
        tokenStore: store,
    }
}

// GetAuthorizationURL は認証 URL を取得
func (m *OAuthManager) GetAuthorizationURL() (string, *oauth.RequestToken, error) {
    requestToken, url, err := m.consumer.GetRequestTokenAndUrl("")
    if err != nil {
        return "", nil, fmt.Errorf("failed to get request token: %w", err)
    }

    return url, requestToken, nil
}

// ExchangeToken は Authorization Code を Access Token に交換
func (m *OAuthManager) ExchangeToken(requestToken *oauth.RequestToken, verifier string) error {
    accessToken, err := m.consumer.AuthorizeToken(requestToken, verifier)
    if err != nil {
        return fmt.Errorf("failed to authorize token: %w", err)
    }

    token := &storage.OAuthToken{
        AccessToken:       accessToken.Token,
        AccessTokenSecret: accessToken.Secret,
        ConsumerKey:       m.consumer.ConsumerKey,
        CreatedAt:         time.Now().Format(time.RFC3339),
    }

    if err := m.tokenStore.Save(token); err != nil {
        return fmt.Errorf("failed to save token: %w", err)
    }

    return nil
}

// GetAccessToken はストアから Access Token を取得
func (m *OAuthManager) GetAccessToken() (*oauth.AccessToken, error) {
    token, err := m.tokenStore.Load()
    if err != nil {
        return nil, fmt.Errorf("failed to load token: %w", err)
    }

    return &oauth.AccessToken{
        Token:  token.AccessToken,
        Secret: token.AccessTokenSecret,
    }, nil
}

// IsAuthenticated は認証済みかチェック
func (m *OAuthManager) IsAuthenticated() bool {
    return m.tokenStore.Exists()
}
```

#### `internal/zaim/client.go`

```go
package zaim

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "sync"
    "time"

    "github.com/mrjones/oauth"
    "go.uber.org/zap"
)

// Client は Zaim API クライアント
type Client struct {
    consumer   *oauth.Consumer
    oauthMgr   *OAuthManager
    httpClient *http.Client
    logger     *zap.Logger

    cache    *cachedData
    cacheMu  sync.RWMutex
    cacheTTL time.Duration
}

type cachedData struct {
    data      *MoneyResponse
    fetchedAt time.Time
}

// NewClient は Client を作成
func NewClient(oauthMgr *OAuthManager, logger *zap.Logger) *Client {
    return &Client{
        consumer:   oauthMgr.consumer,
        oauthMgr:   oauthMgr,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        logger:     logger,
        cacheTTL:   5 * time.Minute,
    }
}

// GetMoneyData は Zaim API からデータ取得 (キャッシュ対応)
func (c *Client) GetMoneyData(ctx context.Context, startDate, endDate string) (*MoneyResponse, error) {
    c.cacheMu.RLock()
    if c.cache != nil && time.Since(c.cache.fetchedAt) < c.cacheTTL {
        c.logger.Debug("cache hit", zap.Time("fetched_at", c.cache.fetchedAt))
        defer c.cacheMu.RUnlock()
        return c.cache.data, nil
    }
    c.cacheMu.RUnlock()

    data, err := c.fetchAllPages(ctx, startDate, endDate)
    if err != nil {
        return nil, err
    }

    c.cacheMu.Lock()
    c.cache = &cachedData{
        data:      data,
        fetchedAt: time.Now(),
    }
    c.cacheMu.Unlock()

    return data, nil
}

// fetchAllPages は全ページ取得 (ページネーション対応)
func (c *Client) fetchAllPages(ctx context.Context, startDate, endDate string) (*MoneyResponse, error) {
    var allMoney []MoneyEntry
    page := 1
    limit := 100
    maxPages := 100

    accessToken, err := c.oauthMgr.GetAccessToken()
    if err != nil {
        return nil, fmt.Errorf("failed to get access token: %w", err)
    }

    for page <= maxPages {
        url := fmt.Sprintf(
            "https://api.zaim.net/v2/home/money?mapping=1&start_date=%s&end_date=%s&limit=%d&page=%d",
            startDate, endDate, limit, page,
        )

        c.logger.Debug("fetching page", zap.Int("page", page))

        resp, err := c.consumer.Get(url, nil, accessToken)
        if err != nil {
            return nil, fmt.Errorf("API request failed (page %d): %w", page, err)
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
            return nil, fmt.Errorf("API returned status %d (page %d)", resp.StatusCode, page)
        }

        var pageData MoneyResponse
        if err := json.NewDecoder(resp.Body).Decode(&pageData); err != nil {
            return nil, fmt.Errorf("failed to decode response (page %d): %w", page, err)
        }

        if len(pageData.Money) == 0 {
            break
        }

        allMoney = append(allMoney, pageData.Money...)

        if len(pageData.Money) < limit {
            break
        }

        page++
    }

    c.logger.Info("fetched all pages",
        zap.Int("total_records", len(allMoney)),
        zap.Int("pages", page-1),
    )

    return &MoneyResponse{Money: allMoney}, nil
}
```

### Phase 4: メトリクス集計実装 (省略 - 変更なし)

Phase 4, 5 は ARCHITECTURE.md に記載の通りで変更ありません。

### Phase 6: HTTP サーバー実装 (修正版、Request Token ストア対応)

#### `internal/server/handlers.go`

```go
package server

import (
    "context"
    "encoding/json"
    "net/http"
    "time"

    "github.com/gorilla/mux"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/yourusername/zaim-prometheus-exporter/internal/storage"
    "github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
    "go.uber.org/zap"
)

// Server は HTTP サーバー
type Server struct {
    router            *mux.Router
    oauthMgr          *zaim.OAuthManager
    requestTokenStore storage.RequestTokenStore
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

func (s *Server) setupRoutes() {
    s.router.HandleFunc("/zaim/auth/login", s.handleLogin).Methods("GET")
    s.router.HandleFunc("/zaim/auth/callback", s.handleCallback).Methods("GET")
    s.router.HandleFunc("/zaim/auth/status", s.handleStatus).Methods("GET")
    s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
    s.router.Handle("/metrics", promhttp.Handler()).Methods("GET")
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

    if err := s.requestTokenStore.Save(ctx, requestToken); err != nil {
        s.logger.Error("failed to save request token", zap.Error(err))
        http.Error(w, "Failed to save token", http.StatusInternalServerError)
        return
    }

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

    requestToken, err := s.requestTokenStore.Load(ctx, oauthToken)
    if err != nil {
        s.logger.Error("failed to load request token", zap.Error(err))
        http.Error(w, "Invalid or expired oauth_token", http.StatusBadRequest)
        return
    }

    if err := s.oauthMgr.ExchangeToken(requestToken, oauthVerifier); err != nil {
        s.logger.Error("failed to exchange token", zap.Error(err))
        http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
        return
    }

    _ = s.requestTokenStore.Delete(ctx, oauthToken)

    s.logger.Info("OAuth authentication successful")

    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write([]byte(`
        <h1>認証成功</h1>
        <p>Zaim との連携が完了しました。</p>
        <p><a href="/zaim/auth/status">認証状態を確認</a></p>
    `))
}

// handleStatus は認証状態確認
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
    authenticated := s.oauthMgr.IsAuthenticated()

    status := map[string]interface{}{
        "authenticated": authenticated,
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(status)
}

// handleHealth はヘルスチェック
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    health := map[string]interface{}{
        "status":        "ok",
        "authenticated": s.oauthMgr.IsAuthenticated(),
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(health)
}

// Router は mux.Router を返す
func (s *Server) Router() *mux.Router {
    return s.router
}
```

### Phase 7: CLI サブコマンド実装 (暗号化キーローテーション対応)

#### `cmd/exporter/commands.go` (新規)

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"

    "github.com/yourusername/zaim-prometheus-exporter/internal/storage"
)

// runEncryptToken はトークンを暗号化
func runEncryptToken(inputFile, outputFile, encryptionKey string) error {
    // 平文読み込み
    data, err := os.ReadFile(inputFile)
    if err != nil {
        return fmt.Errorf("failed to read input file: %w", err)
    }

    // トークンパース
    var token storage.OAuthToken
    if err := json.Unmarshal(data, &token); err != nil {
        return fmt.Errorf("failed to parse token: %w", err)
    }

    // 暗号化して保存
    store := storage.NewTokenStore(outputFile, encryptionKey)
    if err := store.Save(&token); err != nil {
        return fmt.Errorf("failed to save encrypted token: %w", err)
    }

    fmt.Printf("✅ Token encrypted and saved to %s\n", outputFile)
    return nil
}

// runDecryptToken はトークンを復号化
func runDecryptToken(inputFile, outputFile, encryptionKey string) error {
    // 暗号化トークン読み込み
    store := storage.NewTokenStore(inputFile, encryptionKey)
    token, err := store.Load()
    if err != nil {
        return fmt.Errorf("failed to load encrypted token: %w", err)
    }

    // 平文で保存
    data, err := json.MarshalIndent(token, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal token: %w", err)
    }

    if err := os.WriteFile(outputFile, data, 0600); err != nil {
        return fmt.Errorf("failed to write output file: %w", err)
    }

    fmt.Printf("✅ Token decrypted and saved to %s\n", outputFile)
    return nil
}
```

#### `cmd/exporter/main.go` (修正版、サブコマンド対応)

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "strings"
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

func main() {
    _ = godotenv.Load()

    // サブコマンド処理
    if len(os.Args) > 1 {
        switch os.Args[1] {
        case "encrypt-token":
            cmd := flag.NewFlagSet("encrypt-token", flag.ExitOnError)
            input := cmd.String("input", "", "Input file (plaintext JSON)")
            output := cmd.String("output", "", "Output file (encrypted)")
            key := cmd.String("key", "", "Encryption key (base64)")
            cmd.Parse(os.Args[2:])

            if err := runEncryptToken(*input, *output, *key); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(1)
            }
            return

        case "decrypt-token":
            cmd := flag.NewFlagSet("decrypt-token", flag.ExitOnError)
            input := cmd.String("input", "", "Input file (encrypted)")
            output := cmd.String("output", "", "Output file (plaintext JSON)")
            key := cmd.String("key", "", "Encryption key (base64)")
            cmd.Parse(os.Args[2:])

            if err := runDecryptToken(*input, *output, *key); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(1)
            }
            return

        case "health":
            // ヘルスチェック (Docker HEALTHCHECK 用)
            resp, err := http.Get("http://localhost:8080/health")
            if err != nil || resp.StatusCode != 200 {
                os.Exit(1)
            }
            return
        }
    }

    // 通常の HTTP サーバー起動
    runServer()
}

func runServer() {
    logger, _ := zap.NewProduction()
    defer logger.Sync()

    config := loadConfig()

    tokenStore := storage.NewTokenStore(config.TokenFile, config.EncryptionKey)

    oauthMgr := zaim.NewOAuthManager(
        config.ConsumerKey,
        config.ConsumerSecret,
        config.CallbackURL,
        tokenStore,
    )

    zaimClient := zaim.NewClient(oauthMgr, logger)

    aggregator := metrics.NewAggregator()
    collector := metrics.NewZaimCollector(zaimClient, aggregator, logger)
    prometheus.MustRegister(collector)

    // Request Token ストア初期化
    var requestTokenStore storage.RequestTokenStore
    if redisURL := config.RedisURL; redisURL != "" {
        store, err := storage.NewRedisRequestTokenStore(redisURL, 10*time.Minute)
        if err != nil {
            logger.Fatal("failed to initialize redis store", zap.Error(err))
        }
        defer store.Close()
        requestTokenStore = store
        logger.Info("using redis for request token storage")
    } else {
        requestTokenStore = storage.NewMemoryRequestTokenStore()
        logger.Warn("using in-memory request token storage (not suitable for multiple instances)")
    }

    srv := server.NewServer(oauthMgr, requestTokenStore, logger)

    httpServer := &http.Server{
        Addr:         fmt.Sprintf(":%d", config.Port),
        Handler:      srv.Router(),
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    go func() {
        logger.Info("starting server", zap.Int("port", config.Port))
        if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            logger.Fatal("server failed", zap.Error(err))
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    logger.Info("shutting down server...")

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if err := httpServer.Shutdown(ctx); err != nil {
        logger.Fatal("server forced to shutdown", zap.Error(err))
    }

    logger.Info("server exited")
}

type Config struct {
    ConsumerKey    string
    ConsumerSecret string
    CallbackURL    string
    TokenFile      string
    EncryptionKey  string
    RedisURL       string
    Port           int
}

func loadConfig() *Config {
    return &Config{
        ConsumerKey:    getEnv("ZAIM_CONSUMER_KEY", ""),
        ConsumerSecret: getEnv("ZAIM_CONSUMER_SECRET", ""),
        CallbackURL:    getEnv("ZAIM_CALLBACK_URL", "http://localhost:8080/zaim/auth/callback"),
        TokenFile:      getEnv("TOKEN_FILE", "/data/oauth_tokens.json"),
        EncryptionKey:  getSecretOrEnv("ENCRYPTION_KEY", ""),
        RedisURL:       getEnv("REDIS_URL", ""),
        Port:           getEnvInt("PORT", 8080),
    }
}

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

## ビルドとテスト

### ローカルビルド

```bash
# ビルド
go build -o bin/zaim-exporter ./cmd/exporter

# サブコマンド確認
./bin/zaim-exporter encrypt-token --help
./bin/zaim-exporter decrypt-token --help

# 実行
export ZAIM_CONSUMER_KEY="your_key"
export ZAIM_CONSUMER_SECRET="your_secret"
export ZAIM_CALLBACK_URL="http://localhost:8080/zaim/auth/callback"
export TOKEN_FILE="./tokens.json"
export REDIS_URL=""  # 空 = メモリ使用

./bin/zaim-exporter
```

### 暗号化キーローテーションテスト

```bash
# 1. 暗号化キー生成
OLD_KEY=$(openssl rand -base64 32)
NEW_KEY=$(openssl rand -base64 32)

# 2. 既存トークンを復号化
./bin/zaim-exporter decrypt-token \
    --input ./tokens.json \
    --output /tmp/tokens-plaintext.json \
    --key "$OLD_KEY"

# 3. 新しいキーで再暗号化
./bin/zaim-exporter encrypt-token \
    --input /tmp/tokens-plaintext.json \
    --output ./tokens.json \
    --key "$NEW_KEY"

# 4. 平文ファイル削除
rm /tmp/tokens-plaintext.json
```

## 次のステップ

実装が完了したら、[DOCKER.md](./DOCKER.md) で Docker 化と Traefik 統合を行います。
