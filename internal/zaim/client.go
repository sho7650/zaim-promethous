package zaim

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dghubble/oauth1"
	"go.uber.org/zap"
)

const (
	baseURL = "https://api.zaim.net/v2/home"
)

// TransactionFetcher は取引データ取得の抽象化インターフェース
// テスタビリティのため、具体的な実装（Client）から分離
type TransactionFetcher interface {
	GetCurrentMonthTransactions(ctx context.Context) ([]Transaction, error)
}

type Client struct {
	httpClient *http.Client
	logger     *zap.Logger
}

// Client が TransactionFetcher を実装していることをコンパイル時に保証
var _ TransactionFetcher = (*Client)(nil)

func NewClient(config *oauth1.Config, token *oauth1.Token, logger *zap.Logger) *Client {
	httpClient := config.Client(context.Background(), token)
	httpClient.Timeout = 30 * time.Second

	return &Client{
		httpClient: httpClient,
		logger:     logger,
	}
}

type MoneyData struct {
	Money []Transaction `json:"money"`
}

type Transaction struct {
	ID            int64  `json:"id"`
	Mode          string `json:"mode"`          // "payment", "income", "transfer"
	UserID        int    `json:"user_id"`
	Date          string `json:"date"`          // "2024-01-15"
	FromAccountID int    `json:"from_account_id"`
	ToAccountID   int    `json:"to_account_id,omitempty"`
	Amount        int    `json:"amount"`
	Comment       string `json:"comment"`
	Name          string `json:"name"`
	Place         string `json:"place"`
	Created       string `json:"created"`       // "2024-01-15 10:30:45"
	Updated       string `json:"updated"`       // "2024-01-15 10:30:45"
}

func (c *Client) GetTransactions(ctx context.Context, startDate, endDate time.Time) ([]Transaction, error) {
	url := fmt.Sprintf("%s/money?mapping=1&start_date=%s&end_date=%s&limit=100",
		baseURL,
		startDate.Format("2006-01-02"),
		endDate.Format("2006-01-02"))

	c.logger.Info("fetching transactions from Zaim API",
		zap.String("start_date", startDate.Format("2006-01-02")),
		zap.String("end_date", endDate.Format("2006-01-02")))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var data MoneyData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Info("successfully fetched transactions",
		zap.Int("count", len(data.Money)))

	return data.Money, nil
}

func (c *Client) GetCurrentMonthTransactions(ctx context.Context) ([]Transaction, error) {
	now := time.Now()
	location, _ := time.LoadLocation("Asia/Tokyo")
	nowJST := now.In(location)

	// Get first and last day of current month
	year, month, _ := nowJST.Date()
	startDate := time.Date(year, month, 1, 0, 0, 0, 0, location)
	endDate := startDate.AddDate(0, 1, -1)

	return c.GetTransactions(ctx, startDate, endDate)
}