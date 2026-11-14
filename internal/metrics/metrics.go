// Package metrics holds handbill's Prometheus metrics. They are served at
// /metrics on a separate listener (METRICS_ADDR), never on the public
// muxes; see docs/design.md#operations.
//
// Collectors are package variables so any package can count without
// plumbing; counting is cheap and harmless when the listener is off.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ferrule-io/handbill/internal/server"
)

// Registry holds every handbill collector plus the Go runtime and process
// collectors.
var Registry = prometheus.NewRegistry()

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_http_requests_total",
		Help: "HTTP requests by host class (app, sites), route pattern, and status code.",
	}, []string{"host", "route", "code"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "handbill_http_request_duration_seconds",
		Help:    "HTTP request latency by host class and route pattern, until the handler returns.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"host", "route"})

	// MCPToolCalls counts tool calls by tool and outcome: ok, rejected
	// (an error the model sees, such as invalid input or a missing role),
	// or error (an internal failure).
	MCPToolCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_mcp_tool_calls_total",
		Help: "MCP tool calls by tool and outcome (ok, rejected, error).",
	}, []string{"tool", "outcome"})

	// Publishes counts finalized deployments by outcome: ok, rejected
	// (invalid or over a limit), or error.
	Publishes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_publishes_total",
		Help: "Deployment finalizations by outcome (ok, rejected, error).",
	}, []string{"outcome"})

	// RateLimited counts requests refused by a rate-limit rule.
	RateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_rate_limited_total",
		Help: "Requests answered 429 by rate-limit rule.",
	}, []string{"rule"})

	// MaintenanceRuns counts maintenance passes by outcome: ok, error, or
	// skipped (another replica holds the lock).
	MaintenanceRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_maintenance_runs_total",
		Help: "Maintenance passes by outcome (ok, error, skipped).",
	}, []string{"outcome"})
	// MaintenanceDeleted counts rows and blobs removed by maintenance job.
	MaintenanceDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "handbill_maintenance_deleted_total",
		Help: "Rows (or blobs, for blob_gc) removed or abandoned by maintenance job.",
	}, []string{"job"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequests, httpDuration, MCPToolCalls, Publishes, RateLimited, MaintenanceRuns, MaintenanceDeleted,
	)
}

// Handler serves the registry in the Prometheus exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}

// Register counts requests on both muxes when METRICS_ADDR is set. The
// route label is the mux pattern that serves the request ("unmatched" for
// none), so path parameters never become label values.
func Register(r *server.Routes, d *server.Deps) error {
	if d.Config.Operations.MetricsAddr == "" {
		return nil
	}
	r.UseApp(instrument("app", r.App))
	r.UseSites(instrument("sites", r.Sites))
	return nil
}

func instrument(host string, mux *http.ServeMux) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			route := Route(mux, r)
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			httpRequests.WithLabelValues(host, route, strconv.Itoa(sw.status)).Inc()
			httpDuration.WithLabelValues(host, route).Observe(time.Since(start).Seconds())
		})
	}
}

// Route returns the pattern mux would serve r with, or "unmatched".
func Route(mux *http.ServeMux, r *http.Request) string {
	if _, p := mux.Handler(r); p != "" {
		return p
	}
	return "unmatched"
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses working.
func (w *statusWriter) Flush() {
	w.wroteHeader = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the connection (deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// PoolCollector exports pgxpool statistics.
type PoolCollector struct{ pool *pgxpool.Pool }

// NewPoolCollector returns a collector over pool's statistics.
func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector { return &PoolCollector{pool: pool} }

var (
	poolAcquired = prometheus.NewDesc("handbill_db_pool_acquired_connections", "Connections currently in use.", nil, nil)
	poolIdle     = prometheus.NewDesc("handbill_db_pool_idle_connections", "Idle connections.", nil, nil)
	poolTotal    = prometheus.NewDesc("handbill_db_pool_total_connections", "Open connections.", nil, nil)
	poolMax      = prometheus.NewDesc("handbill_db_pool_max_connections", "Maximum pool size.", nil, nil)
	poolAcquires = prometheus.NewDesc("handbill_db_pool_acquires_total", "Successful connection acquisitions.", nil, nil)
	poolWaits    = prometheus.NewDesc("handbill_db_pool_empty_acquires_total", "Acquisitions that waited for a connection.", nil, nil)
	poolCanceled = prometheus.NewDesc("handbill_db_pool_canceled_acquires_total", "Acquisitions canceled while waiting.", nil, nil)
	poolWaitTime = prometheus.NewDesc("handbill_db_pool_acquire_seconds_total", "Time spent acquiring connections.", nil, nil)
)

// Describe implements prometheus.Collector.
func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolAcquired, poolIdle, poolTotal, poolMax, poolAcquires, poolWaits, poolCanceled, poolWaitTime} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(poolAcquired, float64(s.AcquiredConns()))
	gauge(poolIdle, float64(s.IdleConns()))
	gauge(poolTotal, float64(s.TotalConns()))
	gauge(poolMax, float64(s.MaxConns()))
	counter(poolAcquires, float64(s.AcquireCount()))
	counter(poolWaits, float64(s.EmptyAcquireCount()))
	counter(poolCanceled, float64(s.CanceledAcquireCount()))
	counter(poolWaitTime, s.AcquireDuration().Seconds())
}
