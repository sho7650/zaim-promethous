package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
	"go.uber.org/zap"
)

// Manager manages the lifecycle of Prometheus collectors
// Supports dynamic registration and unregistration of collectors
type Manager struct {
	mu               sync.RWMutex
	currentCollector prometheus.Collector
	registerer       prometheus.Registerer
	logger           *zap.Logger
	aggregator       *Aggregator
}

// NewManager creates a new registry manager
// registerer: prometheus.Registerer interface for testability
// In production, use prometheus.DefaultRegisterer
// In tests, use prometheus.NewRegistry() for isolation
func NewManager(registerer prometheus.Registerer, logger *zap.Logger) *Manager {
	return &Manager{
		registerer: registerer,
		logger:     logger,
		aggregator: NewAggregator(),
	}
}

// RegisterCollector registers a new Zaim collector
// Automatically unregisters existing collector if present
// This enables dynamic collector registration after OAuth authentication
func (m *Manager) RegisterCollector(client zaim.TransactionFetcher) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Unregister existing collector if present
	if m.currentCollector != nil {
		m.registerer.Unregister(m.currentCollector)
		m.logger.Info("unregistered existing collector")
	}

	// Create and register new collector
	collector := NewZaimCollector(client, m.aggregator, m.logger)
	if err := m.registerer.Register(collector); err != nil {
		return err
	}

	m.currentCollector = collector
	m.logger.Info("registered new Zaim collector")
	return nil
}

// UnregisterCollector removes the current collector from the registry
// Called during authentication reset to prevent stale metrics
func (m *Manager) UnregisterCollector() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentCollector != nil {
		m.registerer.Unregister(m.currentCollector)
		m.currentCollector = nil
		m.logger.Info("unregistered collector")
	}
}

// IsRegistered returns whether a collector is currently registered
func (m *Manager) IsRegistered() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentCollector != nil
}
