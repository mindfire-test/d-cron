// Package metrics exposes Prometheus metrics for scheduler and job behaviour.
//
// It lives in its own package so applications that do not use it do not link
// prometheus/client_golang (SDS NFR-402). This package is Phase 2 work and
// currently contains no implementation.
package metrics
