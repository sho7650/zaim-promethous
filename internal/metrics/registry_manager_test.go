package metrics

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
	"go.uber.org/zap"
)

// mockTransactionFetcher は zaim.TransactionFetcher の安全なテスト実装
// HTTPクライアントを必要とせず、パニックを引き起こさない
type mockTransactionFetcher struct {
	transactions []zaim.Transaction
	err          error
}

func (m *mockTransactionFetcher) GetCurrentMonthTransactions(ctx context.Context) ([]zaim.Transaction, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.transactions, nil
}

// newMockFetcher は成功ケース用のモックを生成
func newMockFetcher() zaim.TransactionFetcher {
	return &mockTransactionFetcher{
		transactions: []zaim.Transaction{
			{
				ID:     1,
				Mode:   "payment",
				Date:   "2024-01-15",
				Amount: 1000,
				Name:   "Test Transaction",
			},
		},
	}
}

// newErrorFetcher はエラーケース用のモックを生成
func newErrorFetcher() zaim.TransactionFetcher {
	return &mockTransactionFetcher{
		err: errors.New("API error"),
	}
}

func TestManager_RegisterCollector(t *testing.T) {
	t.Run("初回登録", func(t *testing.T) {
		// 各テストで独立したレジストリを使用してグローバル汚染を防ぐ
		registry := prometheus.NewRegistry()
		manager := NewManager(registry, zap.NewNop())
		fetcher := newMockFetcher()

		err := manager.RegisterCollector(fetcher)
		assert.NoError(t, err)
		assert.True(t, manager.IsRegistered())
	})

	t.Run("既存Collectorの置換", func(t *testing.T) {
		// 独立したレジストリ使用
		registry := prometheus.NewRegistry()
		manager := NewManager(registry, zap.NewNop())

		// 1回目の登録
		err := manager.RegisterCollector(newMockFetcher())
		assert.NoError(t, err)
		assert.True(t, manager.IsRegistered())

		// 2回目の登録（既存を自動解除して置換）
		err = manager.RegisterCollector(newMockFetcher())
		assert.NoError(t, err)
		assert.True(t, manager.IsRegistered())
	})

	t.Run("Collectorの解除", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		manager := NewManager(registry, zap.NewNop())

		// 登録
		err := manager.RegisterCollector(newMockFetcher())
		assert.NoError(t, err)
		assert.True(t, manager.IsRegistered())

		// 解除
		manager.UnregisterCollector()
		assert.False(t, manager.IsRegistered())
	})

	t.Run("登録前の解除は安全", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		manager := NewManager(registry, zap.NewNop())

		// 何も登録されていない状態で解除してもパニックしない
		manager.UnregisterCollector()
		assert.False(t, manager.IsRegistered())
	})

	t.Run("エラー時のCollector動作", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		manager := NewManager(registry, zap.NewNop())

		// エラーを返すfetcherでもCollector登録は成功
		// （エラーは Collect() 実行時に zaim_error メトリクスとして処理される）
		err := manager.RegisterCollector(newErrorFetcher())
		assert.NoError(t, err)
		assert.True(t, manager.IsRegistered())
	})
}

func TestManager_ConcurrentAccess(t *testing.T) {
	registry := prometheus.NewRegistry()
	manager := NewManager(registry, zap.NewNop())

	// 複数ゴルーチンから同時に登録/解除を実行
	// sync.RWMutex により安全に処理される
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetcher := newMockFetcher()
			_ = manager.RegisterCollector(fetcher)
			manager.UnregisterCollector()
		}()
	}
	wg.Wait()

	// レース検出なしで完了することを確認
	// go test -race で実行すること
	assert.False(t, manager.IsRegistered())
}

func TestManager_MultipleUnregister(t *testing.T) {
	registry := prometheus.NewRegistry()
	manager := NewManager(registry, zap.NewNop())

	// 登録
	err := manager.RegisterCollector(newMockFetcher())
	assert.NoError(t, err)

	// 複数回解除してもパニックしない
	manager.UnregisterCollector()
	manager.UnregisterCollector()
	manager.UnregisterCollector()

	assert.False(t, manager.IsRegistered())
}
