// Package ui provides an embedded, server-rendered dashboard for monitoring d-cron.
//
// The dashboard is completely self-contained with no external CDN or asset build
// dependencies, allowing it to operate in air-gapped environments. Handler returns
// a read-only HTTP handler that displays the current leadership state, resolved
// advisory-lock key, registered jobs with their configured schedules and recent outcomes,
// and execution history when persistence is enabled.
//
// The dashboard does not provide built-in authentication or authorization: securing
// the route and mounting it behind appropriate middleware is the responsibility of
// the host application.
package ui

import "embed"

// Assets holds the embedded dashboard assets (HTML, CSS, and JS). It is
// exported for mounting directly on custom application routers if needed.
//
//go:embed assets
var Assets embed.FS
