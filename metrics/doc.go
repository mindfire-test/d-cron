// Package metrics exposes observability counters and gauges for scheduler and
// job behaviour (SDS §11, issue #36).
//
// It lives in its own package so applications that do not use it do not link
// any metrics SDK (SDS NFR-402). The core scheduler emits observability
// signals through the Recorder interface defined here; an application wires a
// Recorder (e.g. a Prometheus adapter) in with dcron.WithMetrics.
package metrics
