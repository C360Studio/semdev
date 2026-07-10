// Package health classifies a service's health from its resource pressure.
package health

// Status is a service's coarse health classification.
type Status string

// The health classifications, worst to best.
const (
	Unhealthy Status = "unhealthy"
	Degraded  Status = "degraded"
	Healthy   Status = "healthy"
)

const (
	warningThreshold  = 0.75
	criticalThreshold = 0.90
)

// Classify reports a service's health from its CPU and memory utilization, each a ratio
// in [0, 1]. The worse of the two dimensions decides the class: utilization at or above
// the critical threshold is Unhealthy, at or above the warning threshold is Degraded,
// and anything lower is Healthy.
func Classify(cpu, mem float64) Status {
	pressure := cpu
	if mem > pressure {
		pressure = mem
	}
	switch {
	case pressure >= criticalThreshold:
		return Unhealthy
	case pressure > warningThreshold:
		return Degraded
	default:
		return Healthy
	}
}
