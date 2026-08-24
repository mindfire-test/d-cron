// Package ui serves the embedded server-rendered dashboard.
//
// The dashboard is embedded so it works air-gapped with no CDN and no build
// step (SDS FR-407). Handler returns a read-only http.Handler (Phase 2,
// NFR-502) that renders leadership state, the resolved advisory-lock key, the
// registered jobs with their schedules and last outcomes, and — when history
// is enabled — recent executions.
//
// The surface performs no authentication or authorization: mounting it at an
// appropriate path and protecting it is the host application's responsibility
// (SDS FR-408).
package ui

import "embed"

// Assets holds the embedded dashboard assets (HTML, CSS, and JS). It is
// exported for mounting on the application router once the dashboard is
// implemented.
//
//go:embed assets
var Assets embed.FS
