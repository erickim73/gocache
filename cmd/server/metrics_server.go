package main

import (
	"time"
	"net/http"
	"log/slog"
	"fmt"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// creates and starts an http server that exposes prometheus metrics
func StartMetricsServer(port int) {
	slog.Info("Starting metrics server", "port", port)

	// create new http mux (router) for metrics server. avoids conflicts with any other http servers that might be running
	mux := http.NewServeMux()

	// register /metrics endpoint
	// returns an http handler that 1. collects all metrics from prometheus registry, formats them in prometheus text exposition format, serves them over http
	mux.Handle("/metrics", promhttp.Handler())

	// add health check endpoint for metrics server
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// add a root endpoint with helpful information
	// when users navigate to http://localhost:9090/, they'll see instructions
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := fmt.Sprintf(`
<DOCTYPE html>
<html>
<head>
	<title>GoCache Metrics</title>
</head>
<body>
	<h1>GoCache Metrics Server</h1>
	<p>This server exposes Prometheus metrics for monitoring.</p>
	<ul>
		<li><a href="/metrics">Metrics Endpoint</a> - Prometheus metrics in text format</li>
		<li><a href="/health">Health Check</a> - Server health status</li>
	</ul>
	<h2>Usage</h2>
	<p>Configure Prometheus to scrape: <code>http://localhost:%d/metrics</code></p>
	<h2>Available Metrics</h2>
	<ul>
		<li><strong>gocache_operations_total</strong> - Total cache operations by type</li>
		<li><strong>gocache_cache_hits_total</strong> - Total cache hits</li>
		<li><strong>gocache_cache_misses_total</strong> - Total cache misses</li>
		<li><strong>gocache_operation_duration_seconds</strong> - Operation latency histogram</li>
		<li><strong>gocache_memory_bytes</strong> - Current memory usage</li>
		<li><strong>gocache_items_count</strong> - Number of items in cache</li>
		<li><strong>gocache_evictions_total</strong> - Total LRU evictions</li>
		<li><strong>gocache_expirations_total</strong> - Total TTL expirations</li>
		<li><strong>gocache_active_connections</strong> - Active client connections</li>
	</ul>
</body>
</html>
`, port)
		w.Write([]byte(html))
	})

	// create http server with reasonable timeouts
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", port),
		Handler: mux,

		// max duration for reading the entire request
		ReadTimeout: 15 * time.Second,
		
		// max duration for writing response
		WriteTimeout: 15 * time.Second,

		// how long to keep idle connections open
		IdleTimeout: 60 * time.Second,
	}

	// log startup message
	slog.Info("Metrics server ready",
		"metrics_url", fmt.Sprintf("http://0.0.0.0:%d/metrics", port),
		"health_url", fmt.Sprintf("http://0.0.0.0:%d/health", port),
		"web_ui", fmt.Sprintf("http://0.0.0.0:%d/", port),
	)

	// start server. ListenAndServe blocks until server stops or encounters an error
	err := server.ListenAndServe()

	// server stopped
	if err != nil && err != http.ErrServerClosed {
		slog.Error("Metrics server failed",
			"error", err,
			"port", port,
		)	
	}
}