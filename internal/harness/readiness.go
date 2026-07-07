package harness

// Readiness is the T5 verification-capability decision (design D8): before dev
// builds toward a claim, is there a tier that can PROVE it, and where?
type Readiness string

// The three readiness classifications.
const (
	// ReadinessReady — a sandbox-scope tier proves the claim: build, the harness
	// stamps the measurement (G3), and the clean room verifies it (G4).
	ReadinessReady Readiness = "ready"
	// ReadinessDeferred — only an operator-ci tier proves it: build, but the proof
	// is deferred-and-noted (G7), never gated in-sandbox and never faked.
	ReadinessDeferred Readiness = "deferred"
	// ReadinessPark — no tier proves it: park toward the human rather than fake a
	// proof the sandbox cannot produce.
	ReadinessPark Readiness = "park"
)

// AssessReadiness reads the manifest's declared tiers and returns the T5 verdict
// (D8). A sandbox tier wins (Ready); absent that, an operator-ci tier defers the
// proof; absent any tier, the claim parks toward the human. It never invents a
// tier or gates on evidence the sandbox cannot produce — the discipline that keeps
// semdev out of the unwinnable "prove live/heavy behavior in-sandbox" trap.
func AssessReadiness(tiers []Tier) Readiness {
	hasSandbox := false
	hasOperatorCI := false
	for _, t := range tiers {
		switch t.Scope {
		case TierSandbox:
			hasSandbox = true
		case TierOperatorCI:
			hasOperatorCI = true
		}
	}
	switch {
	case hasSandbox:
		return ReadinessReady
	case hasOperatorCI:
		return ReadinessDeferred
	default:
		return ReadinessPark
	}
}
