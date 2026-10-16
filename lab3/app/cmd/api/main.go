package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"shop/internal/store"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shop_api_http_requests_total",
		Help: "HTTP requests by route and status code.",
	}, []string{"route", "method", "code"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "shop_api_http_request_duration_seconds",
		Help:    "HTTP request latency by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	ordersCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_api_orders_created_total",
		Help: "Orders successfully written to the database.",
	})
)

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func instrument(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		h(rec, r)
		httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.code)).Inc()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

type server struct {
	// db пуст, пока не удалось подключиться к postgres.
	db         atomic.Pointer[store.Store]
	healthFail bool
	version    string
}

// dbOrUnavailable отдаёт 503, если база ещё не подключена.
func (s *server) dbOrUnavailable(w http.ResponseWriter) *store.Store {
	db := s.db.Load()
	if db == nil {
		writeError(w, http.StatusServiceUnavailable, "database not ready")
	}
	return db
}

// health намеренно не ходит в базу: liveness не должен рестартить
// все поды api, если недоступен postgres.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if s.healthFail {
		http.Error(w, "unhealthy (HEALTH_FAIL=true)", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-App-Version", s.version)
	_, _ = w.Write([]byte("ok"))
}

type createOrderRequest struct {
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Item == "" || req.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "item must be non-empty and quantity > 0")
		return
	}

	db := s.dbOrUnavailable(w)
	if db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	order, err := db.CreateOrder(ctx, req.Item, req.Quantity)
	if err != nil {
		slog.Error("create order", "err", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	ordersCreated.Inc()
	writeJSON(w, http.StatusCreated, order)
}

func (s *server) listOrders(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be in 1..1000")
			return
		}
		limit = n
	}

	db := s.dbOrUnavailable(w)
	if db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	orders, err := db.ListOrders(ctx, limit)
	if err != nil {
		slog.Error("list orders", "err", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

	srv := &server{
		healthFail: os.Getenv("HEALTH_FAIL") == "true",
		version:    getenv("APP_VERSION", "dev"),
	}

	// Подключаемся к базе в фоне: HTTP (и /health для проб) поднимается сразу,
	// даже если postgres ещё не развёрнут. До подключения /order и /orders отдают 503.
	go func() {
		st, err := store.Connect(ctx, dsn)
		if err != nil {
			slog.Error("connect to database", "err", err)
			return
		}
		srv.db.Store(st)
		slog.Info("database connected")
	}()
	defer func() {
		if st := srv.db.Load(); st != nil {
			st.Close()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", instrument("/health", srv.health))
	mux.HandleFunc("POST /order", instrument("/order", srv.createOrder))
	mux.HandleFunc("GET /orders", instrument("/orders", srv.listOrders))
	mux.Handle("GET /metrics", promhttp.Handler())

	httpSrv := &http.Server{
		Addr:              ":" + getenv("PORT", "8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("api listening", "addr", httpSrv.Addr, "version", srv.version, "health_fail", srv.healthFail)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	// Graceful shutdown: дописываем текущие запросы перед выходом,
	// чтобы rolling update не ронял запросы в полёте.
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}
