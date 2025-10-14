package metrics

import (
	"fmt"
	"time"

	"github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
)

type Aggregator struct{}

func NewAggregator() *Aggregator {
	return &Aggregator{}
}

type HourlyMetrics struct {
	Hour         time.Time
	PaymentCount int
	PaymentTotal int
	IncomeCount  int
	IncomeTotal  int
}

type DailyMetrics struct {
	Date         time.Time
	PaymentCount int
	PaymentTotal int
	IncomeCount  int
	IncomeTotal  int
}

func (a *Aggregator) AggregateByHour(transactions []zaim.Transaction) map[string]*HourlyMetrics {
	metrics := make(map[string]*HourlyMetrics)
	location, _ := time.LoadLocation("Asia/Tokyo")

	for _, tx := range transactions {
		// Parse created timestamp
		createdTime, err := time.ParseInLocation("2006-01-02 15:04:05", tx.Created, location)
		if err != nil {
			continue
		}

		// Round to hour
		hour := time.Date(
			createdTime.Year(),
			createdTime.Month(),
			createdTime.Day(),
			createdTime.Hour(),
			0, 0, 0, location,
		)

		key := hour.Format("2006-01-02 15:00:00")
		if _, exists := metrics[key]; !exists {
			metrics[key] = &HourlyMetrics{Hour: hour}
		}

		switch tx.Mode {
		case "payment":
			metrics[key].PaymentCount++
			metrics[key].PaymentTotal += tx.Amount
		case "income":
			metrics[key].IncomeCount++
			metrics[key].IncomeTotal += tx.Amount
		}
	}

	return metrics
}

func (a *Aggregator) AggregateByDay(transactions []zaim.Transaction) map[string]*DailyMetrics {
	metrics := make(map[string]*DailyMetrics)
	location, _ := time.LoadLocation("Asia/Tokyo")

	for _, tx := range transactions {
		// Parse date
		date, err := time.ParseInLocation("2006-01-02", tx.Date, location)
		if err != nil {
			continue
		}

		key := date.Format("2006-01-02")
		if _, exists := metrics[key]; !exists {
			metrics[key] = &DailyMetrics{Date: date}
		}

		switch tx.Mode {
		case "payment":
			metrics[key].PaymentCount++
			metrics[key].PaymentTotal += tx.Amount
		case "income":
			metrics[key].IncomeCount++
			metrics[key].IncomeTotal += tx.Amount
		}
	}

	return metrics
}

func (a *Aggregator) GetTodayTotal(transactions []zaim.Transaction) int {
	location, _ := time.LoadLocation("Asia/Tokyo")
	today := time.Now().In(location).Format("2006-01-02")

	total := 0
	for _, tx := range transactions {
		if tx.Date == today && tx.Mode == "payment" {
			total += tx.Amount
		}
	}

	return total
}

func (a *Aggregator) GeneratePrometheusMetrics(hourlyMetrics map[string]*HourlyMetrics, todayTotal int) string {
	output := "# HELP zaim_payment_amount Total payment amount per hour\n"
	output += "# TYPE zaim_payment_amount gauge\n"

	for hour, metrics := range hourlyMetrics {
		output += fmt.Sprintf("zaim_payment_amount{hour=\"%s\"} %d\n", hour, metrics.PaymentTotal)
	}

	output += "\n# HELP zaim_payment_count Number of payments per hour\n"
	output += "# TYPE zaim_payment_count gauge\n"

	for hour, metrics := range hourlyMetrics {
		output += fmt.Sprintf("zaim_payment_count{hour=\"%s\"} %d\n", hour, metrics.PaymentCount)
	}

	output += "\n# HELP zaim_income_amount Total income amount per hour\n"
	output += "# TYPE zaim_income_amount gauge\n"

	for hour, metrics := range hourlyMetrics {
		output += fmt.Sprintf("zaim_income_amount{hour=\"%s\"} %d\n", hour, metrics.IncomeTotal)
	}

	output += "\n# HELP zaim_income_count Number of income transactions per hour\n"
	output += "# TYPE zaim_income_count gauge\n"

	for hour, metrics := range hourlyMetrics {
		output += fmt.Sprintf("zaim_income_count{hour=\"%s\"} %d\n", hour, metrics.IncomeCount)
	}

	output += "\n# HELP zaim_today_total_amount Today's total spending\n"
	output += "# TYPE zaim_today_total_amount gauge\n"
	output += fmt.Sprintf("zaim_today_total_amount %d\n", todayTotal)

	return output
}