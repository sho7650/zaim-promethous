# Zaim Prometheus Exporter - Docker & Traefik Configuration

## Docker 化

### Dockerfile (マルチステージビルド)

`Dockerfile` をプロジェクトルートに作成:

```dockerfile
# ========================================
# Stage 1: Builder
# ========================================
FROM golang:1.25-alpine AS builder

# 必要なパッケージインストール
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# 依存関係のキャッシュ最適化
COPY go.mod go.sum ./
RUN go mod download

# ソースコードコピー
COPY . .

# ビルド
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o zaim-exporter \
    ./cmd/exporter

# ========================================
# Stage 2: Runtime
# ========================================
FROM scratch

# CA 証明書とタイムゾーンデータをコピー
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# 実行ファイルコピー
COPY --from=builder /build/zaim-exporter /zaim-exporter

# データディレクトリ作成
# (実際は Docker Volume でマウントされる)
VOLUME ["/data"]

# ポート公開
EXPOSE 8080

# ヘルスチェック
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD ["/zaim-exporter", "health"] || exit 1

# 実行
ENTRYPOINT ["/zaim-exporter"]
```

**ポイント**:
- **マルチステージビルド**: 最終イメージサイズ ~10MB
- **`scratch` ベース**: 最小限のイメージ、セキュリティリスク最小化
- **静的バイナリ**: CGO 無効化で依存ライブラリなし
- **CA 証明書**: HTTPS 通信のため必須
- **タイムゾーン**: Asia/Tokyo で時刻処理

### .dockerignore

```.dockerignore
# Git
.git
.gitignore

# Documentation
docs/
README.md

# Build artifacts
bin/
*.log

# Local files
.env
tokens.json
*.json

# IDE
.vscode/
.idea/

# macOS
.DS_Store
```

### ビルドとテスト

```bash
# イメージビルド
docker build -t zaim-exporter:latest .

# ローカルテスト
docker run --rm \
    -e ZAIM_CONSUMER_KEY="your_key" \
    -e ZAIM_CONSUMER_SECRET="your_secret" \
    -e ZAIM_CALLBACK_URL="http://localhost:8080/zaim/auth/callback" \
    -v $(pwd)/data:/data \
    -p 8080:8080 \
    zaim-exporter:latest

# ヘルスチェック
curl http://localhost:8080/health
```

## Docker Compose 統合

### 環境別の構成

#### 開発環境 (docker-compose.dev.yml)

**特徴**: シングルインスタンス、メモリベース Request Token ストア、暗号化なし

```yaml
version: '3.9'

services:
  zaim-exporter:
    build: .
    container_name: zaim-exporter-dev
    restart: unless-stopped

    environment:
      - ZAIM_CONSUMER_KEY=${ZAIM_CONSUMER_KEY}
      - ZAIM_CONSUMER_SECRET=${ZAIM_CONSUMER_SECRET}
      - ZAIM_CALLBACK_URL=http://localhost:8080/zaim/auth/callback
      - TOKEN_FILE=/data/oauth_tokens.json
      - ENCRYPTION_KEY=  # 空 = 平文保存
      - REDIS_URL=       # 空 = メモリストア使用
      - PORT=8080
      - TZ=Asia/Tokyo

    volumes:
      - ./data:/data

    ports:
      - "8080:8080"

    networks:
      - default

# Redis なし (メモリストア使用)
```

**使用方法**:
```bash
docker-compose -f docker-compose.dev.yml up
```

#### 本番環境 (docker-compose.yml) - Redis + 複数インスタンス対応

**特徴**: 水平スケーリング対応、Redis ベース Request Token ストア、暗号化有効

```yaml
version: '3.9'

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

    # ヘルスチェック（sh -c で環境変数を展開）
    healthcheck:
      test: ["CMD", "sh", "-c", "redis-cli -a \"$REDIS_PASSWORD\" --no-auth-warning ping"]
      interval: 10s
      timeout: 3s
      retries: 3

    # 代替案: REDISCLI_AUTH 環境変数を使用する方法
    # environment:
    #   - REDISCLI_AUTH=${REDIS_PASSWORD}
    # healthcheck:
    #   test: ["CMD", "redis-cli", "ping"]

  # ========================================
  # Zaim Prometheus Exporter
  # ========================================
  zaim-exporter:
    image: zaim-exporter:latest
    restart: unless-stopped

    # スケールアウト可能
    deploy:
      replicas: 2  # 複数インスタンス

    # 環境変数
    environment:
      - ZAIM_CONSUMER_KEY=${ZAIM_CONSUMER_KEY}
      - ZAIM_CONSUMER_SECRET=${ZAIM_CONSUMER_SECRET}
      - ZAIM_CALLBACK_URL=https://zaim.yourdomain.com/zaim/auth/callback
      - TOKEN_FILE=/data/oauth_tokens.json
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}
      - REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379/0
      - PORT=8080
      - TZ=Asia/Tokyo

    # ボリューム (Access Token 永続化)
    volumes:
      - zaim-tokens:/data

    # ネットワーク
    networks:
      - traefik  # Traefik との接続
      - internal # Redis, Prometheus との接続

    depends_on:
      redis:
        condition: service_healthy

    # Traefik ラベル
    labels:
      # コンテナを Traefik で管理
      - "traefik.enable=true"

      # ========================================
      # OAuth エンドポイント (外部アクセス許可)
      # ========================================
      - "traefik.http.routers.zaim-auth.rule=Host(`zaim.yourdomain.com`) && PathPrefix(`/zaim/auth`)"
      - "traefik.http.routers.zaim-auth.entrypoints=websecure"
      - "traefik.http.routers.zaim-auth.tls=true"
      - "traefik.http.routers.zaim-auth.tls.certresolver=letsencrypt"
      - "traefik.http.routers.zaim-auth.service=zaim-auth-service"
      - "traefik.http.services.zaim-auth-service.loadbalancer.server.port=8080"

      # ========================================
      # メトリクスエンドポイント (内部ネットワークのみ)
      # ========================================
      - "traefik.http.routers.zaim-metrics.rule=Host(`zaim.yourdomain.com`) && Path(`/metrics`)"
      - "traefik.http.routers.zaim-metrics.entrypoints=websecure"
      - "traefik.http.routers.zaim-metrics.tls=true"
      - "traefik.http.routers.zaim-metrics.middlewares=internal-only@docker"
      - "traefik.http.routers.zaim-metrics.service=zaim-metrics-service"
      - "traefik.http.services.zaim-metrics-service.loadbalancer.server.port=8080"

      # IP ホワイトリストミドルウェア
      - "traefik.http.middlewares.internal-only.ipwhitelist.sourcerange=172.16.0.0/12,192.168.0.0/16,127.0.0.1/32"

    # ヘルスチェック
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s

    # セキュリティ
    security_opt:
      - no-new-privileges:true
    read_only: false  # /data への書き込みが必要

# ========================================
# ボリューム
# ========================================
volumes:
  zaim-tokens:
    driver: local
  redis-data:
    driver: local

# ========================================
# ネットワーク
# ========================================
networks:
  traefik:
    external: true  # 既存の Traefik ネットワーク
  internal:
    internal: true  # Redis, Prometheus との内部通信
```

### .env.example

プロジェクトルートに作成:

```bash
# Zaim OAuth Credentials
ZAIM_CONSUMER_KEY=your_consumer_key_here
ZAIM_CONSUMER_SECRET=your_consumer_secret_here

# Redis Password (本番環境)
REDIS_PASSWORD=your_redis_password_here

# Encryption Key (オプション: 32 バイト Base64)
# 生成: openssl rand -base64 32
ENCRYPTION_KEY=

# Domain (Traefik)
ZAIM_DOMAIN=zaim.yourdomain.com
```

### 環境別の .env 設定

#### 開発環境 (.env.dev)

```bash
ZAIM_CONSUMER_KEY=your_key
ZAIM_CONSUMER_SECRET=your_secret
REDIS_PASSWORD=  # 不要 (Redis 使用しない)
ENCRYPTION_KEY=  # 不要 (平文保存)
```

#### 本番環境 (.env)

```bash
ZAIM_CONSUMER_KEY=your_key
ZAIM_CONSUMER_SECRET=your_secret
REDIS_PASSWORD=$(openssl rand -base64 32)  # 強力なパスワード
ENCRYPTION_KEY=$(openssl rand -base64 32)  # 32バイト鍵
```

### Docker Secrets の使用 (推奨: 本番環境)

**セキュリティ強化**: 環境変数の代わりに Docker Secrets を使用することで、機密情報をより安全に管理できます。

#### Secrets の作成

```bash
# ENCRYPTION_KEY シークレット作成
openssl rand -base64 32 | docker secret create encryption_key -

# REDIS_PASSWORD シークレット作成
openssl rand -base64 32 | docker secret create redis_password -
```

#### docker-compose.yml への統合

```yaml
version: '3.9'

services:
  redis:
    image: redis:7-alpine
    secrets:
      - redis_password
    command: sh -c 'redis-server --requirepass "$$(cat /run/secrets/redis_password)"'
    # ... 他の設定 ...

  zaim-exporter:
    image: zaim-exporter:latest
    secrets:
      - encryption_key
      - redis_password
    entrypoint: ["/docker-entrypoint.sh"]
    command: ["/app/zaim-exporter"]
    environment:
      - ZAIM_CONSUMER_KEY=${ZAIM_CONSUMER_KEY}
      - ZAIM_CONSUMER_SECRET=${ZAIM_CONSUMER_SECRET}
      - ZAIM_CALLBACK_URL=https://zaim.yourdomain.com/zaim/auth/callback
      # ENCRYPTION_KEY は /run/secrets/encryption_key から自動読み込み
      # REDIS_URL は entrypoint スクリプトで /run/secrets/redis_password から構築
    # ... 他の設定 ...

secrets:
  encryption_key:
    external: true
  redis_password:
    external: true
```

**注意**: アプリケーションコードは `/run/secrets/encryption_key` を自動的に読み込みます (getSecretOrEnv 関数)。

#### Entrypoint スクリプト (`docker-entrypoint.sh`)

Docker Secrets から `REDIS_URL` を構築するための entrypoint スクリプトが必要です：

```bash
#!/bin/sh
set -e

# Docker Secrets から環境変数を構築
if [ -f /run/secrets/redis_password ]; then
    export REDIS_PASSWORD=$(cat /run/secrets/redis_password)
    export REDIS_URL="redis://:${REDIS_PASSWORD}@redis:6379/0"
fi

# アプリケーションを実行
exec "$@"
```

**Dockerfile に追加**:
```dockerfile
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
```

### セットアップ手順

```bash
# 1. .env ファイル作成
cp .env.example .env
nano .env  # ZAIM_CONSUMER_KEY, ZAIM_CONSUMER_SECRET を設定

# 2. Traefik ネットワーク作成 (まだない場合)
docker network create traefik

# 3. イメージビルド
docker build -t zaim-exporter:latest .

# 4. コンテナ起動
docker-compose up -d

# 5. ログ確認
docker-compose logs -f zaim-exporter

# 6. OAuth 認証
open https://zaim.yourdomain.com/zaim/auth/login

# 7. メトリクス確認 (Prometheus から)
curl http://zaim-exporter:8080/metrics
```

## Traefik 統合詳細

### Traefik の docker-compose.yml (参考)

既存の Traefik 設定に追加:

```yaml
version: '3.9'

services:
  traefik:
    image: traefik:v2.11
    container_name: traefik
    restart: unless-stopped

    command:
      - "--api.dashboard=true"
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
      - "--certificatesresolvers.letsencrypt.acme.email=your@email.com"
      - "--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"

    ports:
      - "80:80"
      - "443:443"

    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - traefik-letsencrypt:/letsencrypt

    networks:
      - traefik

volumes:
  traefik-letsencrypt:

networks:
  traefik:
    external: true
```

### Zaim アプリケーション登録

1. **Zaim Developer にアクセス**: https://dev.zaim.net/
2. **新規アプリケーション作成**:
   - アプリケーション名: `Prometheus Exporter`
   - サービス URL: `https://zaim.yourdomain.com`
   - Callback URL: `https://zaim.yourdomain.com/zaim/auth/callback`
   - アクセスレベル: `Reading records written to your account book` (読み取り専用)
   - 継続アクセス: ✅ チェック (Permanently accessible)
3. **Consumer Key/Secret 取得**: `.env` に設定

### ルーティングの仕組み

```
[Internet]
    ↓ HTTPS (443)
[Traefik]
    ├─ Host: zaim.yourdomain.com
    │   └─ PathPrefix: /zaim/auth
    │       ├─ /zaim/auth/login    → zaim-exporter:8080
    │       ├─ /zaim/auth/callback → zaim-exporter:8080
    │       └─ /zaim/auth/status   → zaim-exporter:8080
    │
    └─ Host: zaim.yourdomain.com
        └─ Path: /metrics
            └─ Middleware: internal-only (IP whitelist)
                └─ zaim-exporter:8080
                    ↑
                    │ HTTP (内部ネットワーク)
                    │
                [Prometheus]
```

## Prometheus 統合

### Prometheus の docker-compose.yml に追加

```yaml
services:
  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    restart: unless-stopped

    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus

    networks:
      - internal  # zaim-exporter と同じネットワーク

    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
      - '--web.console.libraries=/usr/share/prometheus/console_libraries'
      - '--web.console.templates=/usr/share/prometheus/consoles'

volumes:
  prometheus-data:

networks:
  internal:
    external: true
```

### prometheus.yml 設定

```yaml
global:
  scrape_interval: 60s
  evaluation_interval: 60s

scrape_configs:
  # ========================================
  # Zaim Household Budget
  # ========================================
  - job_name: 'zaim-household-budget'
    static_configs:
      - targets: ['zaim-exporter:8080']

    metrics_path: '/metrics'
    scheme: http

    # 認証不要 (内部ネットワーク)
```

### Prometheus で確認

```bash
# Prometheus にアクセス
open http://localhost:9090

# PromQL でクエリ
zaim_today_total_amount
zaim_payment_amount_total

# Grafana ダッシュボード作成
# - 今日の支出: zaim_today_total_amount
# - 時間別支出: sum(zaim_payment_amount_total) by (hour)
# - カテゴリ別支出: sum(zaim_payment_by_category_total) by (category_id)
```

## セキュリティ強化

### 1. トークン暗号化の有効化

```bash
# 暗号化キー生成
openssl rand -base64 32

# .env に追加
ENCRYPTION_KEY=<generated_key>

# コンテナ再起動
docker-compose restart zaim-exporter
```

### 2. ファイアウォール設定 (ホスト)

```bash
# UFW (Ubuntu)
sudo ufw allow 80/tcp   # HTTP (Let's Encrypt)
sudo ufw allow 443/tcp  # HTTPS
sudo ufw enable

# iptables
sudo iptables -A INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 443 -j ACCEPT
sudo iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
sudo iptables -P INPUT DROP
```

### 3. Traefik レート制限

```yaml
# docker-compose.yml (zaim-exporter labels に追加)
labels:
  # レート制限: 100 req/min
  - "traefik.http.middlewares.rate-limit.ratelimit.average=100"
  - "traefik.http.middlewares.rate-limit.ratelimit.burst=50"
  - "traefik.http.routers.zaim-auth.middlewares=rate-limit@docker"
```

### 4. セキュリティヘッダー

```yaml
# docker-compose.yml (zaim-exporter labels に追加)
labels:
  # セキュリティヘッダー
  - "traefik.http.middlewares.security-headers.headers.stsSeconds=31536000"
  - "traefik.http.middlewares.security-headers.headers.stsIncludeSubdomains=true"
  - "traefik.http.middlewares.security-headers.headers.contentTypeNosniff=true"
  - "traefik.http.middlewares.security-headers.headers.browserXssFilter=true"
  - "traefik.http.middlewares.security-headers.headers.frameDeny=true"
  - "traefik.http.routers.zaim-auth.middlewares=security-headers@docker"
```

## 運用管理

### ログ管理

```bash
# リアルタイムログ
docker-compose logs -f zaim-exporter

# JSON ログを解析
docker-compose logs zaim-exporter | jq '.level, .message'

# エラーログのみ
docker-compose logs zaim-exporter | jq 'select(.level == "error")'
```

### バックアップ

```bash
# トークンバックアップスクリプト
cat > /usr/local/bin/backup-zaim-tokens.sh <<'EOF'
#!/bin/bash
BACKUP_DIR=/backup/zaim
mkdir -p $BACKUP_DIR

# Docker Volume からコピー
docker cp zaim-exporter:/data/oauth_tokens.json \
    $BACKUP_DIR/tokens-$(date +%Y%m%d-%H%M%S).json

# 30日以上古いバックアップを削除
find $BACKUP_DIR -name "tokens-*.json" -mtime +30 -delete
EOF

chmod +x /usr/local/bin/backup-zaim-tokens.sh

# cron 登録 (毎日 3時)
echo "0 3 * * * /usr/local/bin/backup-zaim-tokens.sh" | crontab -
```

### リストア

```bash
# バックアップから復元
docker cp /backup/zaim/tokens-20250115-030000.json \
    zaim-exporter:/data/oauth_tokens.json

# コンテナ再起動
docker-compose restart zaim-exporter
```

### アップデート

```bash
# 1. 新しいイメージビルド
docker build -t zaim-exporter:v1.1.0 .

# 2. docker-compose.yml の image を更新
# image: zaim-exporter:v1.1.0

# 3. コンテナ再作成
docker-compose up -d

# 4. 古いイメージ削除
docker image prune -f
```

## トラブルシューティング

### 1. OAuth 認証が失敗する

```bash
# ログ確認
docker-compose logs zaim-exporter | grep -i "oauth"

# Callback URL が正しいか確認
echo $ZAIM_CALLBACK_URL
# → https://zaim.yourdomain.com/zaim/auth/callback と一致しているか

# Zaim アプリ登録の Callback URL と一致しているか確認
```

### 2. メトリクスが取得できない

```bash
# コンテナ内部からアクセス
docker exec -it prometheus wget -O- http://zaim-exporter:8080/metrics

# ネットワーク接続確認
docker exec -it prometheus ping zaim-exporter

# 認証状態確認
curl https://zaim.yourdomain.com/zaim/auth/status
```

### 3. Traefik ルーティングが動作しない

```bash
# Traefik ダッシュボードで確認
open http://localhost:8080/dashboard/  # Traefik API dashboard

# ルーター一覧
docker exec traefik traefik version

# ログ確認
docker-compose logs traefik | grep zaim
```

### 4. トークンファイルが保存されない

```bash
# Volume 確認
docker volume inspect zaim-premetheus_zaim-tokens

# コンテナ内部確認
docker exec -it zaim-exporter ls -la /data

# パーミッション確認
docker exec -it zaim-exporter cat /data/oauth_tokens.json
```

## パフォーマンスチューニング

### キャッシュ TTL 調整

```yaml
# docker-compose.yml
environment:
  - CACHE_TTL=300  # 5分 (デフォルト)
  # 頻繁に更新する場合: 60 (1分)
  # あまり更新しない場合: 900 (15分)
```

### Prometheus スクレイプ間隔

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'zaim-household-budget'
    scrape_interval: 60s  # デフォルト
    # リソース節約: 300s (5分)
    # リアルタイム: 30s
```

### リソース制限

```yaml
# docker-compose.yml
services:
  zaim-exporter:
    deploy:
      resources:
        limits:
          cpus: '0.5'      # CPU 50%
          memory: 128M     # メモリ 128MB
        reservations:
          cpus: '0.1'
          memory: 64M
```

## まとめ

これで Zaim Prometheus Exporter の Docker 化と Traefik 統合が完了しました。

**次のステップ**:
1. `docker-compose up -d` でコンテナ起動
2. `https://zaim.yourdomain.com/zaim/auth/login` で OAuth 認証
3. Prometheus で `zaim_today_total_amount` などを確認
4. Grafana でダッシュボード作成
