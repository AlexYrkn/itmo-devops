package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"shop/internal/store"
)

var (
	ordersProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_worker_orders_processed_total",
		Help: "Orders marked as processed.",
	})

	processErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_worker_process_errors_total",
		Help: "Failed processing iterations.",
	})

	ordersPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_worker_orders_pending",
		Help: "Orders with status 'new' observed on the last iteration.",
	})

	lastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_worker_last_success_timestamp_seconds",
		Help: "Unix time of the last successful processing iteration.",
	})
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		slog.Warn("invalid int env, using default", "key", key, "value", v, "default", def)
	}
	return def
}

func processOnce(ctx context.Context, st *store.Store, batch int) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ids, err := st.ProcessBatch(ctx, batch)
	if err != nil {
		processErrors.Inc()
		slog.Error("process batch", "err", err)
		return
	}
	if len(ids) > 0 {
		ordersProcessed.Add(float64(len(ids)))
		slog.Info("processed orders", "count", len(ids), "ids", ids)
	}

	pending, err := st.CountPending(ctx)
	if err != nil {
		processErrors.Inc()
		slog.Error("count pending", "err", err)
		return
	}
	ordersPending.Set(float64(pending))
	lastSuccess.SetToCurrentTime()
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	interval := time.Duration(getenvInt("POLL_INTERVAL_SECONDS", 5)) * time.Second
	batch := getenvInt("BATCH_SIZE", 10)
	healthFail := os.Getenv("HEALTH_FAIL") == "true"

	// HTTP-сервер для проб и метрик поднимаем до подключения к базе,
	// чтобы под не убивался liveness-пробой, пока ждёт postgres.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if healthFail {
			http.Error(w, "unhealthy (HEALTH_FAIL=true)", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", promhttp.Handler())

	httpSrv := &http.Server{
		Addr:              ":" + getenv("PORT", "8081"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("worker http listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	st, err := store.Connect(ctx, dsn)
	if err != nil {
		slog.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	slog.Info("worker started", "interval", interval.String(), "batch", batch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		processOnce(ctx, st, batch)
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutdownCtx)
			return
		case <-ticker.C:
		}
	}
}
