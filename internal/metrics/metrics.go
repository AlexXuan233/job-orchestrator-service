package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	JobsSubmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_submitted_total",
		Help: "Total number of jobs submitted",
	}, []string{"status"})

	JobsDispatched = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_dispatched_total",
		Help: "Total number of jobs dispatched to workers",
	}, []string{"status"})

	JobFetchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_fetch_duration_seconds",
		Help:    "Time spent fetching URLs per attempt",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"status"})

	JobsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jobs_inflight_total",
		Help: "Total number of jobs currently running",
	})

	DomainInflight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "domain_inflight_current",
		Help: "Current in-flight fetches per domain",
	}, []string{"domain"})

	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "job_queue_depth",
		Help: "Current depth of the pending job queue",
	})
)
