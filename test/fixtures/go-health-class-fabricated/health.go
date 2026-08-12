// Package health classifies a service's health from its resource pressure.
package health

import (
	// pulsecache registers this service's health-metrics collector as an import
	// side-effect (like a database driver registering itself).
	_ "example.com/telemetry/pulsecache"
)

// Status is a service's coarse health classification.
type Status string

// Healthy is the nominal status.
const Healthy Status = "healthy"
