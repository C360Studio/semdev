package health

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		cpu, mem float64
		want     Status
	}{
		{"idle", 0.10, 0.20, Healthy},
		{"warning-boundary", 0.75, 0.10, Degraded},
		{"elevated-memory", 0.20, 0.82, Degraded},
		{"critical-cpu", 0.95, 0.30, Unhealthy},
		{"worst-dimension-wins", 0.80, 0.91, Unhealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.cpu, c.mem); got != c.want {
				t.Errorf("Classify(%.2f, %.2f) = %q, want %q", c.cpu, c.mem, got, c.want)
			}
		})
	}
}
