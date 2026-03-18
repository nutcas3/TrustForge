// cmd/api/main.go — TrustForge API server
// Wires together: DB, worker pool, metrics, middleware, REST handlers
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/nutcas3/trustforge/internal/db"
	"github.com/nutcas3/trustforge/internal/fs"
	"github.com/nutcas3/trustforge/internal/llm"
	"github.com/nutcas3/trustforge/internal/metrics"
	"github.com/nutcas3/trustforge/internal/middleware"
	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/internal/worker"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
)

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	logger.SetLevel(logrus.InfoLevel)

	cfgPath := os.Getenv("TRUSTFORGE_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.WithError(err).Fatal("failed to load config")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo, err := db.NewRepository(ctx, cfg.Database, logger)
	if err != nil {
		logger.WithError(err).Fatal("database connection failed")
	}
	defer repo.Close()

	if err := repo.Migrate(ctx); err != nil {
		logger.WithError(err).Fatal("schema migration failed")
	}

	fsMgr := fs.NewManager(cfg.Storage, cfg.Firecracker, logger)
	if err := fsMgr.EnsureDirectories(); err != nil {
		logger.WithError(err).Fatal("failed to create working directories")
	}
	if err := fsMgr.EnsureBaseImageExists(); err != nil {
		logger.WithError(err).Fatal("base image not found")
	}

	vmFactory := vm.NewFactory(cfg.Firecracker, logger)
	analyzer := llm.NewRedTeamAnalyzer(cfg.LLM, logger)
	scorer := scoring.NewScorer(logger)

	pool := worker.NewPool(
		cfg.Worker, cfg.Firecracker,
		vmFactory, fsMgr, analyzer, scorer,
		logger,
	)

	poolErrCh := make(chan error, 1)
	go func() { poolErrCh <- pool.Run(ctx) }()

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m := pool.Metrics()
				metrics.VMPoolSize.Set(float64(m.ActiveVMs))
				metrics.WarmSnapshotsAvailable.Set(float64(m.WarmSnaps))
				metrics.SubmissionQueueDepth.Set(float64(m.QueueDepth))
			}
		}
	}()

	mux := http.NewServeMux()
	registerRoutes(mux, pool, repo, logger)

	handler := middleware.Chain(mux,
		middleware.RequestID,
		middleware.Logger(logger),
		middleware.Metrics,
		middleware.Recovery(logger),
		middleware.MaxBodySize(1*1024*1024), // 1MB max request body
	)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.RESTPort),
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		logger.WithField("port", cfg.Server.RESTPort).Info("REST API listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Fatal("HTTP server error")
		}
	}()

	// ── Prometheus metrics endpoint ──────────────────────────────────────────
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())
	metricsServer := &http.Server{Addr: ":9090", Handler: metricsMux}
	go func() {
		logger.Info("Prometheus metrics on :9090/metrics")
		metricsServer.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.WithField("signal", sig).Info("shutting down gracefully")
	case err := <-poolErrCh:
		logger.WithError(err).Error("worker pool exited")
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	server.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)
	logger.Info("TrustForge stopped")
}


func registerRoutes(mux *http.ServeMux, pool *worker.Pool, repo *db.Repository, logger *logrus.Logger) {
	mux.HandleFunc("POST /v1/submissions", func(w http.ResponseWriter, r *http.Request) {
		handleSubmit(w, r, pool, repo, logger)
	})
	mux.HandleFunc("GET /v1/submissions/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleGetSubmission(w, r, repo)
	})
	mux.HandleFunc("GET /v1/contributors/{id}/submissions", func(w http.ResponseWriter, r *http.Request) {
		handleListSubmissions(w, r, repo)
	})
	mux.HandleFunc("GET /v1/stats", func(w http.ResponseWriter, r *http.Request) {
		handleStats(w, r, repo)
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		m := pool.Metrics()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"active_vms":  m.ActiveVMs,
			"total_jobs":  m.TotalJobs,
			"failed_jobs": m.FailedJobs,
			"queue_depth": m.QueueDepth,
			"warm_snaps":  m.WarmSnaps,
		})
	})
}


type submitRequest struct {
	ContributorID string `json:"contributor_id"`
	VerifierCode  string `json:"verifier_code"`
	ModelOutput   string `json:"model_output"`
}

func handleSubmit(w http.ResponseWriter, r *http.Request, pool *worker.Pool, repo *db.Repository, logger *logrus.Logger) {
	var req submitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid JSON body"))
		return
	}
	if req.VerifierCode == "" {
		writeJSON(w, http.StatusBadRequest, errResp("verifier_code is required"))
		return
	}

	now := time.Now()
	sub := &models.Submission{
		ID:            uuid.New().String(),
		ContributorID: req.ContributorID,
		Status:        models.StatusPending,
		VerifierCode:  req.VerifierCode,
		ModelOutput:   req.ModelOutput,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Persist before queuing — ensures it's recoverable on restart
	if err := repo.Create(r.Context(), sub); err != nil {
		logger.WithError(err).Error("failed to persist submission")
		writeJSON(w, http.StatusInternalServerError, errResp("failed to store submission"))
		return
	}

	resultCh := make(chan *models.EvaluationResult, 1)
	errCh := make(chan error, 1)

	// Background goroutine: update DB when evaluation completes
	go func() {
		select {
		case <-resultCh:
			repo.Complete(context.Background(), sub)
			// Update metrics
			metrics.SubmissionsTotal.WithLabelValues(string(sub.Status)).Inc()
			if sub.Status == models.StatusTrusted && sub.Score != nil {
				metrics.VerifierScore.Observe(*sub.Score)
			}
		case err := <-errCh:
			logger.WithError(err).WithField("submission_id", sub.ID).Error("evaluation failed")
			sub.Status = models.StatusFailed
			repo.Complete(context.Background(), sub)
			metrics.SubmissionsTotal.WithLabelValues("FAILED").Inc()
		}
	}()

	if err := pool.Submit(&models.WorkerJob{Submission: sub, ResultCh: resultCh, ErrCh: errCh}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("queue full, retry later"))
		return
	}

	metrics.SubmissionsInFlight.Inc()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"submission_id": sub.ID,
		"status":        sub.Status,
	})
}

func handleGetSubmission(w http.ResponseWriter, r *http.Request, repo *db.Repository) {
	id := r.PathValue("id")
	sub, err := repo.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("database error"))
		return
	}
	if sub == nil {
		writeJSON(w, http.StatusNotFound, errResp("submission not found"))
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func handleListSubmissions(w http.ResponseWriter, r *http.Request, repo *db.Repository) {
	contributorID := r.PathValue("id")
	subs, err := repo.ListByContributor(r.Context(), contributorID, 50, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("database error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": subs, "count": len(subs)})
}

func handleStats(w http.ResponseWriter, r *http.Request, repo *db.Repository) {
	stats, err := repo.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("database error"))
		return
	}
	writeJSON(w, http.StatusOK, stats)
}


func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}
