package observability

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry       *prometheus.Registry
	httpRequests   *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	avatarUploads  *prometheus.CounterVec
	avatarDeletes  *prometheus.CounterVec
	workerMessages *prometheus.CounterVec
	workerDuration *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gophprofile_http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gophprofile_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		avatarUploads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gophprofile_avatar_uploads_total",
			Help: "Avatar upload attempts by result.",
		}, []string{"result"}),
		avatarDeletes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gophprofile_avatar_deletes_total",
			Help: "Avatar deletion attempts by result.",
		}, []string{"result"}),
		workerMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gophprofile_worker_messages_total",
			Help: "Worker messages by routing key and result.",
		}, []string{"routing_key", "result"}),
		workerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gophprofile_worker_processing_duration_seconds",
			Help:    "Worker message processing duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"routing_key"}),
	}
	metrics.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.avatarUploads,
		metrics.avatarDeletes,
		metrics.workerMessages,
		metrics.workerDuration,
	)
	return metrics
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		path := r.URL.Path
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		duration := time.Since(started).Seconds()
		status := strconv.Itoa(recorder.status)
		route := r.Pattern
		if route == "" {
			route = path
		}
		m.httpRequests.WithLabelValues(r.Method, route, status).Inc()
		m.httpDuration.WithLabelValues(r.Method, route).Observe(duration)
		slog.InfoContext(r.Context(), "HTTP-запрос обработан",
			"method", r.Method,
			"path", path,
			"route", route,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (m *Metrics) RecordUpload(err error) {
	m.avatarUploads.WithLabelValues(metricResult(err)).Inc()
}

func (m *Metrics) RecordDelete(err error) {
	m.avatarDeletes.WithLabelValues(metricResult(err)).Inc()
}

func (m *Metrics) ObserveWorker(routingKey string, started time.Time, err error) {
	m.workerMessages.WithLabelValues(routingKey, metricResult(err)).Inc()
	m.workerDuration.WithLabelValues(routingKey).Observe(time.Since(started).Seconds())
}

func metricResult(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
