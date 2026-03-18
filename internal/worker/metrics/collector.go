package metrics

import (
	"sync/atomic"
)

// Metrics tracks worker pool statistics
type Metrics struct {
	ActiveVMs  int64
	TotalJobs  int64
	FailedJobs int64
	QueueDepth int
	WarmSnaps  int
}

// Collector tracks atomic metrics for the worker pool
type Collector struct {
	activeVMs  atomic.Int64
	totalJobs  atomic.Int64
	failedJobs atomic.Int64
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{}
}

// IncrementActiveVMs increments the active VM count
func (m *Collector) IncrementActiveVMs() {
	m.activeVMs.Add(1)
}

// DecrementActiveVMs decrements the active VM count
func (m *Collector) DecrementActiveVMs() {
	m.activeVMs.Add(-1)
}

// IncrementTotalJobs increments the total jobs processed
func (m *Collector) IncrementTotalJobs() {
	m.totalJobs.Add(1)
}

// IncrementFailedJobs increments the failed jobs count
func (m *Collector) IncrementFailedJobs() {
	m.failedJobs.Add(1)
}

// GetMetrics returns the current metrics snapshot
func (m *Collector) GetMetrics(queueDepth int, warmSnaps int) Metrics {
	return Metrics{
		ActiveVMs:  m.activeVMs.Load(),
		TotalJobs:  m.totalJobs.Load(),
		FailedJobs: m.failedJobs.Load(),
		QueueDepth: queueDepth,
		WarmSnaps:  warmSnaps,
	}
}
