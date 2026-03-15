// Package metrics provides Prometheus instrumentation for TrustForge.
// All metrics are pre-registered at init time to avoid duplicate registration panics.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// All counters, gauges, and histograms are package-level vars so they can be
// called from any package without passing a metrics object around.

var (
	SubmissionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trustforge_submissions_total",
		Help: "Total number of submissions received, labelled by final status.",
	}, []string{"status"})

	SubmissionsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trustforge_submissions_in_flight",
		Help: "Number of submissions currently being evaluated.",
	})

	SubmissionQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trustforge_submission_queue_depth",
		Help: "Current depth of the worker job queue.",
	})

	StageDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "trustforge_stage_duration_seconds",
		Help:    "Duration of each pipeline stage.",
		Buckets: prometheus.DefBuckets,
	}, []string{"stage"}) // sandbox_prep, vm_resume, execution, red_team, scoring

	VMResumeLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_vm_resume_latency_seconds",
		Help:    "Time to resume a Firecracker VM from snapshot.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
	})

	VMColdBootLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_vm_cold_boot_latency_seconds",
		Help:    "Time to cold-boot a Firecracker VM.",
		Buckets: []float64{0.05, 0.1, 0.125, 0.2, 0.3, 0.5, 1.0, 2.0},
	})

	VMPoolSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trustforge_vm_pool_active",
		Help: "Number of currently active VM instances.",
	})

	WarmSnapshotsAvailable = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trustforge_warm_snapshots_available",
		Help: "Number of warm Firecracker snapshots ready for instant-resume.",
	})

	SnapshotCreationTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trustforge_snapshot_creations_total",
		Help: "Total number of VM snapshots created.",
	})

	VerifierDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_verifier_duration_seconds",
		Help:    "Wall-clock time for verifier.py execution inside the VM.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 15, 20, 25, 30},
	})

	VerifierScore = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_verifier_score",
		Help:    "Distribution of scores returned by trusted verifiers.",
		Buckets: []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})

	VerifierTimeouts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trustforge_verifier_timeouts_total",
		Help: "Number of verifier executions that hit the wall-clock timeout.",
	})

	VerifierMemUsageKB = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_verifier_mem_usage_kb",
		Help:    "Peak RSS memory usage of verifier.py processes (KB).",
		Buckets: []float64{1024, 4096, 16384, 65536, 131072, 262144, 524288},
	})

	RedTeamDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_redteam_duration_seconds",
		Help:    "Time for the LLM red-team analysis to complete.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
	})

	RedTeamRiskScore = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_redteam_risk_score",
		Help:    "Distribution of risk scores assigned by the red-team LLM.",
		Buckets: []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})

	RedTeamRejectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trustforge_redteam_rejections_total",
		Help: "Number of submissions rejected by red-team analysis.",
	})

	TaskDiskCreationDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trustforge_task_disk_creation_seconds",
		Help:    "Time to create and populate an ephemeral task disk.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0},
	})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "trustforge_http_request_duration_seconds",
		Help:    "HTTP request duration, labelled by method, path, and status code.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status_code"})
)

// Handler returns the Prometheus HTTP handler for /metrics
func Handler() http.Handler {
	return promhttp.Handler()
}

// Timer is a helper for recording histogram durations
type Timer struct {
	start     time.Time
	histogram prometheus.Histogram
}

// NewTimer starts a timer for the given histogram
func NewTimer(h prometheus.Histogram) *Timer {
	return &Timer{start: time.Now(), histogram: h}
}

// ObserveDuration records elapsed time since the timer was created
func (t *Timer) ObserveDuration() {
	t.histogram.Observe(time.Since(t.start).Seconds())
}

// StageTimer records a pipeline stage duration using the StageDuration histogram
func StageTimer(stage string) func() {
	start := time.Now()
	return func() {
		StageDuration.WithLabelValues(stage).Observe(time.Since(start).Seconds())
	}
}
