# Critical Fixes for zaim-prometheus-exporter

## 概要

ドキュメントに含まれる3つの重大な問題の修正設計です。これらの問題は全てアプリケーションの起動を阻害します。

---

## 🔴 Issue 1: Missing `strings` Import

### 問題箇所

- **ファイル**: [`docs/IMPLEMENTATION.md:887`](docs/IMPLEMENTATION.md#L887)
- **関数**: `getSecretOrEnv()` at line 1043

### 問題の詳細

```go
// 現在のインポートリスト (line 887-904)
import (
    "context"
    "flag"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"
    // ... ← "strings" が無い
)

// getSecretOrEnv で使用 (line 1043-1051)
func getSecretOrEnv(key, fallback string) string {
    secretPath := fmt.Sprintf("/run/secrets/%s", strings.ToLower(key))  // ← エラー
    if data, err := os.ReadFile(secretPath); err == nil {
        return strings.TrimSpace(string(data))  // ← エラー
    }
    // ...
}
```

**エラーメッセージ**:
```
undefined: strings
```

### 修正方法

インポートリストに `"strings"` を追加（標準ライブラリの alphabetical order に従う）:

```go
import (
    "context"
    "flag"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "strings"  // ← ADD THIS
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
```

### 影響範囲

- ✅ `cmd/exporter/main.go` に同じ修正が必要
- ✅ 実装ガイド `docs/IMPLEMENTATION.md` の修正

### 検証方法

```bash
# コンパイルチェック
go build -o /tmp/zaim-exporter ./cmd/exporter

# 期待: エラーなくビルド成功
```

---

## 🔴 Issue 2: Redis Healthcheck NOAUTH Error

### 問題箇所

- **ファイル**: [`docs/DOCKER.md:183`](docs/DOCKER.md#L183)
- **サービス**: `redis` healthcheck configuration

### 問題の詳細

```yaml
# 現在の設定
redis:
  image: redis:7-alpine
  secrets:
    - redis_password
  command: sh -c 'redis-server --requirepass "$$(cat /run/secrets/redis_password)"'

  healthcheck:
    test: ["CMD", "redis-cli", "--raw", "incr", "ping"]  # ← パスワード無し
    interval: 10s
    timeout: 3s
    retries: 3
```

**問題**:
- Redis は `--requirepass` でパスワード認証が有効
- Healthcheck は認証なしでコマンド実行
- 結果: `NOAUTH Authentication required.` エラー
- サービスが `unhealthy` のままになる
- `depends_on: service_healthy` で zaim-exporter がブロックされる

**実際のエラーログ**:
```
redis_1  | 1:M 13 Oct 2025 12:00:00.000 # NOAUTH Authentication required.
```

### 修正方法

#### Option A: Inline Shell Command (推奨)

```yaml
redis:
  image: redis:7-alpine
  secrets:
    - redis_password
  command: sh -c 'redis-server --requirepass "$$(cat /run/secrets/redis_password)"'

  healthcheck:
    test: ["CMD", "sh", "-c", "redis-cli -a $$(cat /run/secrets/redis_password) --raw incr ping"]
    interval: 10s
    timeout: 3s
    retries: 3
```

**変更点**:
- `sh -c` でシェルコマンドとして実行
- `-a $$(cat /run/secrets/redis_password)` でパスワード認証
- `$$` は docker-compose での `$` エスケープ

#### Option B: REDISCLI_AUTH 環境変数

```yaml
healthcheck:
  test: ["CMD", "sh", "-c", "REDISCLI_AUTH=$$(cat /run/secrets/redis_password) redis-cli --raw incr ping"]
  interval: 10s
  timeout: 3s
  retries: 3
```

#### Option C: Healthcheck Script (カスタムイメージ)

カスタム Redis イメージを作成する場合:

```dockerfile
# Dockerfile.redis
FROM redis:7-alpine

COPY redis-healthcheck.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/redis-healthcheck.sh

CMD ["redis-server"]
```

```bash
#!/bin/sh
# redis-healthcheck.sh
REDIS_PASSWORD=$(cat /run/secrets/redis_password)
redis-cli -a "$REDIS_PASSWORD" --raw incr ping
```

```yaml
redis:
  build:
    context: .
    dockerfile: Dockerfile.redis
  healthcheck:
    test: ["CMD", "/usr/local/bin/redis-healthcheck.sh"]
    interval: 10s
    timeout: 3s
    retries: 3
```

### 推奨ソリューション

**Option A (Inline Shell Command)** を推奨:
- ✅ カスタムイメージ不要
- ✅ 標準的なパターン
- ✅ メンテナンスが容易
- ✅ docker-compose.yml だけで完結

### 影響範囲

- ✅ `docker-compose.yml` の redis service 設定
- ✅ `docs/DOCKER.md` の healthcheck 例

### 検証方法

```bash
# docker-compose で起動
docker-compose up -d redis

# ヘルスチェック状態確認
docker-compose ps
# 期待: redis サービスが "healthy" 状態

# ヘルスチェックログ確認
docker-compose logs redis | grep health
# 期待: エラーなし

# 手動でヘルスチェックコマンド実行
docker-compose exec redis sh -c 'redis-cli -a $(cat /run/secrets/redis_password) --raw incr ping'
# 期待: 数値が返る (例: 1, 2, 3...)
```

---

## 🔴 Issue 3: REDIS_URL Shell Expansion Failure

### 問題箇所

- **ファイル**: [`docs/DOCKER.md:361`](docs/DOCKER.md#L361)
- **サービス**: `zaim-exporter` environment configuration

### 問題の詳細

```yaml
# 現在の設定
zaim-exporter:
  environment:
    - REDIS_URL=redis://:$(cat /run/secrets/redis_password)@redis:6379/0  # ← 展開されない
```

**問題**:
- docker-compose は environment 値で **シェル展開を実行しない**
- 結果: 文字列 `"redis://:$(cat /run/secrets/redis_password)@redis:6379/0"` がそのまま渡される
- Redis 認証が失敗し、アプリケーションが起動しない

**実際の環境変数値**:
```bash
# アプリケーションが受け取る値
REDIS_URL="redis://:$(cat /run/secrets/redis_password)@redis:6379/0"
# ↑ 文字列そのまま、シェル展開されていない
```

### 修正方法

詳細は [`docs/SECRETS_DESIGN.md`](docs/SECRETS_DESIGN.md) を参照してください。

#### 推奨: Go-Based Configuration (コンポーネントベース)

**docker-compose.yml の変更**:

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

    # Redis コンポーネント（パスワードは secrets から自動読み込み）
    - REDIS_HOST=redis
    - REDIS_PORT=6379
    - REDIS_DB=0
    # REDIS_URL は Go コードで自動構築される
```

**Go コードの変更** (`cmd/exporter/main.go`):

```go
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

### アーキテクチャ

```
┌─────────────────────────────────────────┐
│ docker-compose.yml                      │
│ - REDIS_HOST=redis                      │
│ - REDIS_PORT=6379                       │
│ - REDIS_DB=0                            │
│ (REDIS_PASSWORD from secrets)          │
└────────────────┬────────────────────────┘
                 │
                 ↓
┌─────────────────────────────────────────┐
│ Go Application                          │
│ 1. Read REDIS_PASSWORD from            │
│    /run/secrets/redis_password          │
│ 2. Construct REDIS_URL:                 │
│    redis://:PASSWORD@HOST:PORT/DB       │
└─────────────────────────────────────────┘
```

### 利点

✅ **シェル展開不要** - Go コードで構築
✅ **自動 Secrets 読み込み** - `getSecretOrEnv` が処理
✅ **柔軟な設定** - 明示的な `REDIS_URL` でオーバーライド可能
✅ **コンポーネント単位の変更** - 個別設定が容易
✅ **セキュア** - パスワードが docker-compose.yml に含まれない
✅ **テスト可能** - 単体テストが容易

### 影響範囲

- ✅ `cmd/exporter/main.go` - Config 構造体と loadConfig() の拡張
- ✅ `docker-compose.yml` - environment 設定の変更
- ✅ `docs/DOCKER.md` - 例の更新
- ✅ `docs/IMPLEMENTATION.md` - 実装例の更新

### 検証方法

```bash
# 1. Docker secrets 作成
echo "your_redis_password" | docker secret create redis_password -
echo "your_encryption_key" | docker secret create encryption_key -

# 2. アプリケーション起動
docker-compose up -d

# 3. ログ確認（接続成功を確認）
docker-compose logs zaim-exporter | grep -i redis
# 期待: "Connected to Redis" などの成功メッセージ

# 4. Redis 接続テスト
docker-compose exec zaim-exporter sh -c 'echo "Testing Redis connection..."'
# アプリケーション内部で Redis ping が成功することを確認

# 5. 構築された REDIS_URL の確認（デバッグ用）
docker-compose exec zaim-exporter printenv | grep REDIS
# REDIS_HOST=redis
# REDIS_PORT=6379
# REDIS_DB=0
# (REDIS_URL は Go コード内で構築されているため表示されない)
```

---

## 📋 統合テストプラン

### 前提条件

```bash
# Docker secrets 作成
echo "test_redis_password_123" | docker secret create redis_password -
echo "32_byte_encryption_key_here_xxx" | docker secret create encryption_key -

# .env ファイル作成
cat > .env <<EOF
ZAIM_CONSUMER_KEY=your_consumer_key
ZAIM_CONSUMER_SECRET=your_consumer_secret
EOF
```

### テストシナリオ

#### Test 1: ビルドテスト

```bash
# Go コードのコンパイル（strings import 修正後）
cd /path/to/zaim-prometheus-exporter
go build -o /tmp/zaim-exporter ./cmd/exporter

# 期待: エラーなく完了
echo $?  # 0
```

#### Test 2: Redis ヘルスチェックテスト

```bash
# Redis のみ起動
docker-compose up -d redis

# 30秒待機（ヘルスチェックが実行されるまで）
sleep 30

# ヘルスチェック状態確認
docker-compose ps redis
# 期待: State が "Up (healthy)"

# ヘルスチェックログ確認
docker-compose logs redis | grep -i "noauth"
# 期待: "NOAUTH" エラーが無いこと
```

#### Test 3: アプリケーション起動テスト

```bash
# 全サービス起動
docker-compose up -d

# 起動ログ確認
docker-compose logs zaim-exporter | grep -i error
# 期待: Redis 関連のエラーが無いこと

# Redis 接続確認
docker-compose logs zaim-exporter | grep -i "connected to redis"
# 期待: 接続成功メッセージ

# アプリケーション健全性確認
curl http://localhost:8080/health
# 期待: {"status": "healthy"} または 200 OK
```

#### Test 4: 統合動作テスト

```bash
# メトリクスエンドポイント確認
curl http://localhost:8080/metrics | grep zaim_
# 期待: Prometheus メトリクスが出力される

# Redis にデータが保存されているか確認
docker-compose exec redis sh -c 'redis-cli -a $(cat /run/secrets/redis_password) KEYS "*"'
# 期待: セッションキーなどが存在
```

---

## 🔧 修正作業チェックリスト

### Phase 1: コード修正

- [ ] `cmd/exporter/main.go` に `"strings"` import 追加
- [ ] `cmd/exporter/main.go` に Redis コンポーネント設定追加
  - [ ] `Config` 構造体拡張
  - [ ] `buildRedisURL()` 関数実装
  - [ ] `loadConfig()` 更新
- [ ] コンパイルテスト実行

### Phase 2: Docker 設定修正

- [ ] `docker-compose.yml` の Redis healthcheck 修正
- [ ] `docker-compose.yml` の zaim-exporter environment 修正
  - [ ] `REDIS_URL` 削除
  - [ ] `REDIS_HOST`, `REDIS_PORT`, `REDIS_DB` 追加

### Phase 3: ドキュメント更新

- [ ] `docs/IMPLEMENTATION.md` の import 例修正
- [ ] `docs/IMPLEMENTATION.md` の Config 例修正
- [ ] `docs/DOCKER.md` の healthcheck 例修正
- [ ] `docs/DOCKER.md` の environment 例修正

### Phase 4: テスト

- [ ] ビルドテスト実行
- [ ] Redis ヘルスチェックテスト実行
- [ ] アプリケーション起動テスト実行
- [ ] 統合動作テスト実行

---

## 📚 関連ドキュメント

- [docs/SECRETS_DESIGN.md](docs/SECRETS_DESIGN.md) - Docker Secrets 統合の詳細設計
- [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) - 実装ガイド
- [docs/DOCKER.md](docs/DOCKER.md) - Docker 化ガイド

---

## 🔍 トラブルシューティング

### Q1: "undefined: strings" エラーが出る

**A**: `cmd/exporter/main.go` の import リストに `"strings"` を追加してください。

```go
import (
    // ...
    "strings"  // ← この行を追加
    // ...
)
```

### Q2: Redis ヘルスチェックが常に unhealthy

**A**: healthcheck コマンドがパスワード認証していません。

```yaml
# 修正前
test: ["CMD", "redis-cli", "--raw", "incr", "ping"]

# 修正後
test: ["CMD", "sh", "-c", "redis-cli -a $$(cat /run/secrets/redis_password) --raw incr ping"]
```

### Q3: zaim-exporter が "NOAUTH" エラーで起動しない

**A**: REDIS_URL がシェル展開されていません。コンポーネントベースの設定に変更してください。

```yaml
# 修正前
environment:
  - REDIS_URL=redis://:$(cat /run/secrets/redis_password)@redis:6379/0

# 修正後
environment:
  - REDIS_HOST=redis
  - REDIS_PORT=6379
  - REDIS_DB=0
  # Go コードで REDIS_URL を構築
```

### Q4: Docker secrets が読み込めない

**A**: Secrets が正しく作成されているか確認してください。

```bash
# Secrets 確認
docker secret ls

# Secrets 作成（存在しない場合）
echo "your_password" | docker secret create redis_password -
echo "your_key" | docker secret create encryption_key -
```

---

## ⚠️ 注意事項

### セキュリティ

- ❌ パスワードを docker-compose.yml に直接記述しない
- ❌ パスワードをログに出力しない
- ✅ Docker secrets を使用してセンシティブ情報を管理
- ✅ 本番環境では必ず外部 secrets (`external: true`) を使用

### 互換性

- Go 1.25+ (generics, slices パッケージ使用)
- Docker 24.0+
- Docker Compose 2.20+
- Redis 7.0+

### パフォーマンス

- Redis healthcheck の `interval` は 10s が推奨
- `timeout` は 3s で十分
- `retries` は 3 回が標準的
