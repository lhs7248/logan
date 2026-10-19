// Package observability provides bounded metrics and sanitized request logging.
package observability

import (
	"context"
	"database/sql"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type Registry struct {
	oss           *prometheus.HistogramVec
	jobStates     *prometheus.GaugeVec
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	inflight      prometheus.Gauge
	maintenance   *prometheus.CounterVec
	tasks         *prometheus.GaugeVec
	jobs          *prometheus.HistogramVec
	db            *prometheus.GaugeVec
	dbWait        prometheus.Gauge
	dbWaitSeconds prometheus.Gauge
}

func New() *Registry {
	r := &Registry{oss: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "logan_oss_operation_duration_seconds", Help: "OSS method latency; get ends when response headers and body handle are returned, excluding body reads."}, []string{"operation", "outcome"}), jobStates: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "logan_processing_jobs", Help: "Persistent processing jobs by kind and state; pending includes retry_wait."}, []string{"kind", "state"}), registry: prometheus.NewRegistry(), requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "logan_http_requests_total", Help: "Completed requests."}, []string{"method", "route", "status"}), duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "logan_http_duration_seconds", Help: "Request latency."}, []string{"method", "route"}), inflight: prometheus.NewGauge(prometheus.GaugeOpts{Name: "logan_http_inflight", Help: "In flight requests."}), maintenance: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "logan_maintenance_operations_total", Help: "Maintenance operations."}, []string{"operation", "result"}), tasks: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "logan_maintenance_tasks", Help: "Task snapshot."}, []string{"state"}), jobs: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "logan_jobs_duration_seconds", Help: "Job execution duration."}, []string{"kind", "outcome"}), db: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "logan_db_connections", Help: "Database pool connections."}, []string{"state"}), dbWait: prometheus.NewGauge(prometheus.GaugeOpts{Name: "logan_db_wait_total", Help: "Database cumulative waits."}), dbWaitSeconds: prometheus.NewGauge(prometheus.GaugeOpts{Name: "logan_db_wait_seconds_total", Help: "Database cumulative wait duration."})}
	r.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), r.oss, r.jobStates, r.requests, r.duration, r.inflight, r.maintenance, r.tasks, r.jobs, r.db, r.dbWait, r.dbWaitSeconds)
	return r
}
func (r *Registry) HTTPHandler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}
func bounded(s string, allowed ...string) string {
	for _, a := range allowed {
		if s == a {
			return s
		}
	}
	return "other"
}
func (r *Registry) Maintenance(operation, result string) {
	r.maintenance.WithLabelValues(bounded(operation, "cycle", "scan", "claim", "delete", "retry", "temp_sweep", "reconcile", "cleanup", "retention"), bounded(result, "success", "error", "skipped", "failed", "scheduled")).Inc()
}
func (r *Registry) SetMaintenanceSnapshot(pending, deleting, uploading, failed int64) {
	for k, v := range map[string]int64{"pending": pending, "deleting": deleting, "uploading": uploading, "failed": failed} {
		r.tasks.WithLabelValues(k).Set(float64(v))
	}
}
func (r *Registry) ObserveJob(kind, outcome string, d time.Duration) {
	r.jobs.WithLabelValues(bounded(kind, "native", "web", "maintenance", "temp_sweep"), bounded(outcome, "success", "error", "failed", "canceled", "skipped")).Observe(d.Seconds())
}
func (r *Registry) SetDBStats(s sql.DBStats) {
	for k, v := range map[string]int{"open": s.OpenConnections, "in_use": s.InUse, "idle": s.Idle, "max_open": s.MaxOpenConnections} {
		r.db.WithLabelValues(k).Set(float64(v))
	}
	r.dbWait.Set(float64(s.WaitCount))
	r.dbWaitSeconds.Set(s.WaitDuration.Seconds())
}
func (r *Registry) HTTPMiddleware() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		route := c.FullPath()
		if route == "/metrics" {
			c.Next(ctx)
			return
		}
		if route == "" {
			route = "__unmatched__"
		}
		method := bounded(string(c.Method()), "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS")
		start := time.Now()
		r.inflight.Inc()
		defer func() {
			p := recover()
			status := c.Response.StatusCode()
			if p != nil {
				status = 500
			}
			r.inflight.Dec()
			elapsed := time.Since(start)
			r.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
			r.duration.WithLabelValues(method, route).Observe(elapsed.Seconds())
			slog.Info("http request", "method", method, "route", route, "status", status, "duration_ms", elapsed.Milliseconds())
			if p != nil {
				panic(p)
			}
		}()
		c.Next(ctx)
	}
}

// SetJobStates updates a persistent backlog snapshot, including zero values.
func (r *Registry) SetJobStates(kind string, pending, running, failed int64) {
	kind = bounded(kind, "native", "web")
	for state, n := range map[string]int64{"pending": pending, "running": running, "failed": failed} {
		r.jobStates.WithLabelValues(kind, state).Set(float64(n))
	}
}
