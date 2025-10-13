# Zaim Prometheus Exporter - Architecture Design

## プロジェクト概要

Zaim（家計簿サービス）のデータを取得し、Prometheus メトリクス形式で公開する Go アプリケーション。
オンプレミス環境で Docker コンテナとして動作し、Traefik リバースプロキシ配下で運用する。

## 設計決定の背景

### Google Apps Script からの移行理由

| 課題 | Apps Script | Go + Docker (新) |
|------|-------------|------------------|
| 認証の複雑性 | URL パラメータでトークン露出 | 内部ネットワークで認証不要 |
| セキュリティリスク | 公開 URL、ログ露出 | オンプレミス、トークンはボリューム保存 |
| スケーラビリティ | 実行時間制限あり | 制限なし |
| デバッグ | ログ確認が困難 | 標準出力、ファイル、メトリクス |
| インフラ | Google 依存 | 自己管理可能 |

### なぜ Go を選択したか

1. **Prometheus エコシステムの標準**: 公式クライアントライブラリが充実
2. **コンテナ最適**: 静的バイナリ、イメージサイズ ~10MB
3. **高速起動**: 起動時間 < 100ms
4. **並行処理**: goroutine による効率的な API リクエスト処理
5. **型安全**: コンパイル時の型チェック

## システムアーキテクチャ

### 全体構成図

```
┌─────────────────────────────────────────────────────────────┐
│                        Internet                              │
└────────────────────┬────────────────────────────────────────┘
                     │ HTTPS (固定グローバル IP)
                     │ Port: 443
                     ↓
┌─────────────────────────────────────────────────────────────┐
│              Traefik Reverse Proxy (Container)               │
│  - Let's Encrypt SSL 自動更新                                 │
│  - PathPrefix ルーティング                                    │
│  - IP ホワイトリスト (メトリクスエンドポイント)                  │
└────────┬────────────────────────────────────────────────────┘
         │
         ├─ /zaim/auth/*  (外部アクセス許可)
         │     ↓
         │  [zaim-exporter:8080]
         │     ├─ /zaim/auth/login      ← ユーザーがアクセス
         │     ├─ /zaim/auth/callback   ← Zaim からコールバック
         │     └─ /zaim/auth/status     ← 認証状態確認
         │
         └─ /metrics  (内部ネットワークのみ)
               ↓
            [zaim-exporter:8080]
               └─ /metrics              ← Prometheus がスクレイプ
                     ↑
                     │ HTTP (内部ネットワーク)
                     │
            [prometheus:9090]
```

### ネットワークセキュリティモデル

```yaml
外部アクセス:
  - /zaim/auth/*  # OAuth フローのみ
  - Zaim API からのコールバック受信

内部ネットワーク:
  - /metrics      # Prometheus スクレイプ (認証不要)
  - 同一 Docker ネットワーク内での通信

アクセス制御:
  - Traefik ミドルウェアで IP ホワイトリスト
  - メトリクスエンドポイントは 172.16.0.0/12, 192.168.0.0/16 のみ許可
```

## データフロー

### OAuth 認証フロー

```
1. ユーザー操作
   https://zaim.yourdomain.com/zaim/auth/login
        ↓
2. アプリケーション
   Zaim Request Token 取得
   https://api.zaim.net/v2/auth/request
        ↓
3. リダイレクト
   https://auth.zaim.net/users/auth?oauth_token=xxx
        ↓
4. ユーザー認可 (Zaim サイト)
        ↓
5. コールバック
   https://zaim.yourdomain.com/zaim/auth/callback?oauth_token=xxx&oauth_verifier=yyy
        ↓
6. アプリケーション
   Access Token 取得・保存
   /data/oauth_tokens.json (Docker Volume)
```

### メトリクス取得フロー

```
1. Prometheus (scrape_interval: 60s)
   GET http://zaim-exporter:8080/metrics
        ↓
2. zaim-exporter
   - トークンファイル読み込み (/data/oauth_tokens.json)
   - Zaim API 呼び出し (キャッシュ: 5分)
   - データ集計・加工
        ↓
3. Zaim API
   GET https://api.zaim.net/v2/home/money
   - OAuth 1.0a 署名検証
   - データ返却 (JSON)
        ↓
4. zaim-exporter
   - 時間別集計 (hourly)
   - カテゴリ別集計
   - Prometheus 形式に変換
        ↓
5. Prometheus
   メトリクス収集・保存
```

## コンポーネント設計

### Go アプリケーション構造

```
zaim-prometheus-exporter/
├── cmd/
│   └── exporter/
│       └── main.go                    # エントリーポイント
│           - 環境変数読み込み
│           - HTTP サーバー起動
│           - Prometheus メトリクス登録
│
├── internal/
│   ├── zaim/
│   │   ├── client.go                  # Zaim API クライアント
│   │   │   - OAuth 署名付きリクエスト
│   │   │   - リトライ・レート制限対応
│   │   │   - ページネーション処理
│   │   │
│   │   ├── oauth.go                   # OAuth 1.0a フロー管理
│   │   │   - Request Token 取得
│   │   │   - Access Token 取得
│   │   │   - トークンストアとの連携
│   │   │
│   │   └── models.go                  # データモデル定義
│   │       - MoneyData, Payment, Income, Transfer
│   │       - Category, Account
│   │
│   ├── metrics/
│   │   ├── collector.go               # Prometheus Collector 実装
│   │   │   - prometheus.Collector インターフェース
│   │   │   - Describe(), Collect() メソッド
│   │   │
│   │   └── aggregator.go              # データ集計ロジック
│   │       - 時間別集計 (hourly)
│   │       - カテゴリ別集計
│   │       - 今日の支出計算 (バグ修正済み)
│   │
│   ├── storage/
│   │   └── token_store.go             # トークン永続化
│   │       - JSON ファイル読み書き
│   │       - オプション: AES-256-GCM 暗号化
│   │       - スレッドセーフな読み書き
│   │
│   └── server/
│       └── handlers.go                # HTTP ハンドラー
│           - GET  /zaim/auth/login    (OAuth 開始)
│           - GET  /zaim/auth/callback (OAuth コールバック)
│           - GET  /zaim/auth/status   (認証状態)
│           - GET  /metrics            (Prometheus)
│           - GET  /health             (ヘルスチェック)
│
├── Dockerfile                         # マルチステージビルド
├── docker-compose.yml                 # Traefik 統合
├── .env.example                       # 環境変数テンプレート
├── go.mod                             # Go モジュール定義
└── README.md                          # セットアップ手順
```

## データモデル

### Zaim API レスポンス

```go
type MoneyResponse struct {
    Money []MoneyEntry `json:"money"`
}

type MoneyEntry struct {
    ID              int64     `json:"id"`
    Mode            string    `json:"mode"`      // "payment", "income", "transfer"
    Amount          int       `json:"amount"`
    Date            string    `json:"date"`      // "2025-01-15"
    Created         string    `json:"created"`   // "2025-01-15 14:30:00"
    CategoryID      int       `json:"category_id"`
    GenreID         int       `json:"genre_id"`
    FromAccountID   int       `json:"from_account_id"`
    ToAccountID     int       `json:"to_account_id,omitempty"`
    Comment         string    `json:"comment,omitempty"`
    Name            string    `json:"name"`
    Place           string    `json:"place,omitempty"`
}
```

### Prometheus メトリクス

```go
// 支出合計 (時間別)
zaim_payment_amount_total{hour="2025-01-15 14:00:00"} 3500

// 支出件数 (時間別)
zaim_payment_count_total{hour="2025-01-15 14:00:00"} 2

// カテゴリ別支出
zaim_payment_by_category_total{category="食費",genre="外食"} 1500
zaim_payment_by_category_total{category="交通費",genre="電車"} 2000

// 今日の支出合計
zaim_today_total_amount 3500

// 収入合計 (時間別)
zaim_income_amount_total{hour="2025-01-15 10:00:00"} 50000

// 振替合計 (時間別)
zaim_transfer_amount_total{hour="2025-01-15 09:00:00",from_account="銀行A",to_account="銀行B"} 10000

// API 呼び出しメトリクス
zaim_api_requests_total{endpoint="/v2/home/money",status="200"} 150
zaim_api_request_duration_seconds{endpoint="/v2/home/money",quantile="0.5"} 0.3
zaim_api_request_duration_seconds{endpoint="/v2/home/money",quantile="0.99"} 1.2
```

## トークン永続化設計

### ストレージ方式: Docker Volume + JSON

**選択理由**:
- ✅ 軽量: データベース不要
- ✅ シンプル: ファイル I/O のみ
- ✅ 永続化: コンテナ再起動でも保持
- ✅ バックアップ容易: ファイルコピー
- ✅ デバッグ可能: JSON 直接確認

### ファイル構造

```json
{
  "access_token": "abc123...",
  "access_token_secret": "xyz789...",
  "created_at": "2025-01-15T10:30:00Z",
  "expires_at": null,
  "scope": "read",
  "consumer_key": "your_consumer_key"
}
```

### 暗号化 (オプション)

環境変数 `ENCRYPTION_KEY` が設定されている場合、AES-256-GCM で暗号化:

```go
type EncryptedTokenFile struct {
    Ciphertext string `json:"ciphertext"`  // Base64 エンコード
    Nonce      string `json:"nonce"`       // Base64 エンコード
    Version    string `json:"version"`     // "aes-256-gcm"
}
```

**設定されていない場合**: 平文保存（内部ネットワークのみなので許容）

### ファイルパーミッション

```bash
# コンテナ内
/data/oauth_tokens.json
- Owner: exporter:exporter (UID 1000)
- Permission: 0600 (rw-------)
```

## セキュリティ設計

### 脅威モデル

| 脅威 | 対策 | 優先度 |
|------|------|--------|
| OAuth トークン漏洩 | Docker Volume (ホスト保護), オプション暗号化 | 🔴 High |
| 不正な Metrics アクセス | Traefik IP ホワイトリスト | 🟡 Medium |
| Zaim API 認証情報露出 | 環境変数、コンテナ内のみ | 🔴 High |
| DDoS (OAuth エンドポイント) | Traefik レート制限 | 🟢 Low |
| コンテナ脱出 | 非 root ユーザー実行 | 🟡 Medium |

### セキュリティ対策

#### 1. 環境変数による機密情報管理

```bash
# .env (ホストマシンのみ、コンテナには環境変数として渡す)
ZAIM_CONSUMER_KEY=your_consumer_key
ZAIM_CONSUMER_SECRET=your_consumer_secret
ENCRYPTION_KEY=32_byte_hex_string  # オプション
```

#### 2. Traefik ミドルウェア

```yaml
# docker-compose.yml
labels:
  # OAuth エンドポイント: 外部アクセス許可
  - "traefik.http.routers.zaim-auth.rule=Host(`zaim.yourdomain.com`) && PathPrefix(`/zaim/auth`)"
  - "traefik.http.routers.zaim-auth.entrypoints=websecure"
  - "traefik.http.routers.zaim-auth.tls.certresolver=letsencrypt"

  # メトリクスエンドポイント: 内部ネットワークのみ
  - "traefik.http.routers.zaim-metrics.rule=Host(`zaim.yourdomain.com`) && Path(`/metrics`)"
  - "traefik.http.routers.zaim-metrics.middlewares=internal-only@docker"
  - "traefik.http.middlewares.internal-only.ipwhitelist.sourcerange=172.16.0.0/12,192.168.0.0/16,127.0.0.1/32"
```

#### 3. コンテナセキュリティ

```dockerfile
# 非 root ユーザーで実行
RUN adduser -D -u 1000 exporter
USER exporter

# 読み取り専用ルートファイルシステム (オプション)
docker run --read-only --tmpfs /tmp zaim-exporter
```

#### 4. ネットワーク分離

```yaml
# docker-compose.yml
networks:
  traefik:
    external: true  # 外部ネットワーク (Traefik 接続)
  internal:
    internal: true  # 内部ネットワーク (Prometheus のみ)
```

## パフォーマンス設計

### キャッシュ戦略

**Zaim API レスポンスキャッシュ**: 5分間

理由:
- Zaim API は今月のデータを返す
- 家計簿データはリアルタイム性不要
- API レート制限対策（未公表だが推定 60 req/hour）

```go
type CachedData struct {
    Data      *MoneyResponse
    FetchedAt time.Time
    TTL       time.Duration  // 5 minutes
}

func (c *Client) GetMoneyData(ctx context.Context) (*MoneyResponse, error) {
    if c.cache.IsValid() {
        return c.cache.Data, nil
    }

    // API 呼び出し
    data, err := c.fetchFromAPI(ctx)
    if err != nil {
        return nil, err
    }

    c.cache.Update(data)
    return data, nil
}
```

### ページネーション

Zaim API は 1 リクエストあたり最大 100 件返却。月次データが 100 件を超える場合はページネーション必要。

```go
func (c *Client) GetAllMoneyData(ctx context.Context, startDate, endDate string) ([]MoneyEntry, error) {
    var allData []MoneyEntry
    page := 1
    limit := 100

    for {
        resp, err := c.fetchPage(ctx, startDate, endDate, page, limit)
        if err != nil {
            return nil, err
        }

        if len(resp.Money) == 0 {
            break  // 最終ページ
        }

        allData = append(allData, resp.Money...)

        if len(resp.Money) < limit {
            break  // 最終ページ
        }

        page++

        if page > 100 {
            // 安全装置: 最大 10,000 件
            break
        }
    }

    return allData, nil
}
```

### メトリクス生成の最適化

```go
// 集計結果をメモリ上で事前計算
type AggregatedMetrics struct {
    HourlyPayments   map[string]float64        // hour -> total_amount
    CategoryPayments map[CategoryKey]float64    // (category, genre) -> total_amount
    TodayTotal       float64
    LastUpdated      time.Time
}

// Prometheus Collector は事前計算済みの値を返すだけ
func (c *ZaimCollector) Collect(ch chan<- prometheus.Metric) {
    metrics := c.aggregator.GetCachedMetrics()

    for hour, amount := range metrics.HourlyPayments {
        ch <- prometheus.MustNewConstMetric(
            c.hourlyPaymentDesc,
            prometheus.CounterValue,
            amount,
            hour,
        )
    }
    // ...
}
```

## 運用設計

### ヘルスチェック

```go
// GET /health
type HealthResponse struct {
    Status       string    `json:"status"`        // "ok" or "error"
    OAuthStatus  string    `json:"oauth_status"`  // "authenticated" or "not_authenticated"
    LastAPICall  time.Time `json:"last_api_call,omitempty"`
    CacheStatus  string    `json:"cache_status"`  // "valid" or "expired"
    Uptime       string    `json:"uptime"`
}
```

```yaml
# docker-compose.yml
healthcheck:
  test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/health"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 10s
```

### ログ設計

**標準出力**: 構造化ログ (JSON)

```json
{
  "timestamp": "2025-01-15T14:30:00Z",
  "level": "info",
  "message": "Zaim API request succeeded",
  "duration_ms": 320,
  "endpoint": "/v2/home/money",
  "status_code": 200,
  "records_fetched": 45
}
```

**ログレベル**:
- `DEBUG`: API リクエスト詳細、キャッシュヒット/ミス
- `INFO`: 正常な動作（起動、API 成功、OAuth 完了）
- `WARN`: リトライ、レート制限接近
- `ERROR`: API エラー、トークン無効、ファイル I/O エラー

### メトリクス監視

Prometheus で監視すべきメトリクス:

```promql
# API エラー率
rate(zaim_api_requests_total{status!="200"}[5m])

# API レスポンスタイム
histogram_quantile(0.99, zaim_api_request_duration_seconds)

# 認証状態
zaim_oauth_authenticated  # 1 = 認証済み, 0 = 未認証

# 最終取得時刻
time() - zaim_last_successful_fetch_timestamp_seconds > 600  # 10分以上前
```

### バックアップ

```bash
# OAuth トークンバックアップ (cron)
0 3 * * * docker cp zaim-exporter:/data/oauth_tokens.json /backup/zaim-tokens-$(date +\%Y\%m\%d).json
```

## スケーラビリティ

### 現状の制約

- **単一インスタンス**: OAuth トークンが 1 ファイルのため
- **単一ユーザー**: Zaim アカウント 1 つのみサポート

### 将来の拡張性

#### マルチユーザー対応

```go
// トークンストアを Map で管理
type MultiUserTokenStore struct {
    tokens map[string]*OAuthToken  // user_id -> token
    mu     sync.RWMutex
}

// メトリクスにユーザーラベル追加
zaim_payment_amount_total{user="user1",hour="2025-01-15 14:00:00"} 3500
zaim_payment_amount_total{user="user2",hour="2025-01-15 14:00:00"} 1200
```

#### 水平スケーリング (複数インスタンス)

- **トークンストア**: ファイル → Redis/PostgreSQL
- **キャッシュ**: ローカル → Redis
- **ロードバランサー**: Traefik ラウンドロビン

## 技術選定まとめ

| 技術 | 選択理由 | 代替案 |
|------|---------|--------|
| **Go** | Prometheus 親和性、静的バイナリ、高速起動 | Node.js, Python |
| **mrjones/oauth** | OAuth 1.0a 標準実装、安定性 | dghubble/oauth1 |
| **gorilla/mux** | 柔軟なルーティング、ミドルウェア対応 | chi, gin |
| **Docker Volume** | シンプル、永続化、バックアップ容易 | PostgreSQL, Redis |
| **Traefik** | 既存インフラ、自動 SSL、IP 制限 | nginx, Caddy |
| **JSON ファイル** | 軽量、デバッグ容易、暗号化オプション | SQLite, BadgerDB |

## 次のステップ

1. **実装ガイド作成**: [IMPLEMENTATION.md](./IMPLEMENTATION.md) で段階的な実装手順を提供
2. **Docker 構成作成**: [DOCKER.md](./DOCKER.md) で Traefik 統合を詳細解説
3. **Go コード実装**: プロジェクト構造とすべてのファイルを生成
