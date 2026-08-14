// Package otel wires the OpenTelemetry SDK for scheduler and job tracing.
//
// It lives in its own package so applications that do not use it do not link
// the OTel SDK (SDS §9). This package is Phase 3 work and currently contains
// no implementation.
package otel
