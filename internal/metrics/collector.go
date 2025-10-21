package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
	"go.uber.org/zap"
)

type ZaimCollector struct {
	client        zaim.TransactionFetcher
	aggregator    *Aggregator
	logger        *zap.Logger
	mu            sync.RWMutex
	cache         *metricsCache
	cacheDuration time.Duration
}

type metricsCache struct {
	data      []zaim.Transaction
	timestamp time.Time
}

func NewZaimCollector(client zaim.TransactionFetcher, aggregator *Aggregator, logger *zap.Logger) *ZaimCollector {
	return &ZaimCollector{
		client:        client,
		aggregator:    aggregator,
		logger:        logger,
		cacheDuration: 5 * time.Minute,
	}
}

func (c *ZaimCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func (c *ZaimCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	transactions, err := c.getTransactions(ctx)
	if err != nil {
		c.logger.Error("failed to get transactions", zap.Error(err))
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("zaim_error", "Error fetching data from Zaim API", []string{"type"}, nil),
			prometheus.GaugeValue,
			1,
			"api_error",
		)
		return
	}

	// Aggregate metrics
	hourlyMetrics := c.aggregator.AggregateByHour(transactions)
	todayTotal := c.aggregator.GetTodayTotal(transactions)

	// Export hourly payment metrics
	for hour, metrics := range hourlyMetrics {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("zaim_payment_amount", "Total payment amount per hour", []string{"hour"}, nil),
			prometheus.GaugeValue,
			float64(metrics.PaymentTotal),
			hour,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("zaim_payment_count", "Number of payments per hour", []string{"hour"}, nil),
			prometheus.GaugeValue,
			float64(metrics.PaymentCount),
			hour,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("zaim_income_amount", "Total income amount per hour", []string{"hour"}, nil),
			prometheus.GaugeValue,
			float64(metrics.IncomeTotal),
			hour,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("zaim_income_count", "Number of income transactions per hour", []string{"hour"}, nil),
			prometheus.GaugeValue,
			float64(metrics.IncomeCount),
			hour,
		)
	}

	// Export today's total
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("zaim_today_total_amount", "Today's total spending", nil, nil),
		prometheus.GaugeValue,
		float64(todayTotal),
	)

	// Export last update time
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("zaim_last_update", "Unix timestamp of last successful update", nil, nil),
		prometheus.GaugeValue,
		float64(time.Now().Unix()),
	)
}

func (c *ZaimCollector) getTransactions(ctx context.Context) ([]zaim.Transaction, error) {
	c.mu.RLock()
	if c.cache != nil && time.Since(c.cache.timestamp) < c.cacheDuration {
		c.logger.Debug("using cached transactions")
		data := c.cache.data
		c.mu.RUnlock()
		return data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.cache != nil && time.Since(c.cache.timestamp) < c.cacheDuration {
		return c.cache.data, nil
	}

	transactions, err := c.client.GetCurrentMonthTransactions(ctx)
	if err != nil {
		return nil, err
	}

	c.cache = &metricsCache{
		data:      transactions,
		timestamp: time.Now(),
	}

	c.logger.Info("fetched and cached transactions", zap.Int("count", len(transactions)))
	return transactions, nil
}