// Package ui serves the embedded server-rendered dashboard.
//
// The dashboard is embedded so it works air-gapped with no CDN and no build
// step (SDS FR-407). The dashboard itself is Phase 2 work; this package
// currently only exposes the embedded assets.
//
// Intended: `go:embed assets; var Assets embed.FS` mounted on the application
// router.
package ui

import "embed"

// Assets holds the embedded dashboard assets (HTML, CSS, and JS). It is
// exported for mounting on the application router once the dashboard is
// implemented.
//
//go:embed assets
var Assets embed.FS
