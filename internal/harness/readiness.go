package harness

import "slices"

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

// AssessReadiness answers D8's CLAIM-SPECIFIC question: is there a tier that proves
// the given claim, and where? Only a tier that actually proves the claim counts — a
// sandbox tier that proves the claim wins (Ready); absent that, an operator-ci tier
// that proves it defers the proof (Deferred, never faked); absent any tier that
// proves it, the claim parks toward the human (Park). An unrelated sandbox tier
// (e.g. a unit suite) does NOT make a claim ready that only an operator-ci tier
// (e.g. SITL/hardware) proves — that separation is the whole point of the gate
// (keeping semdev out of the unwinnable "prove live/heavy behavior in-sandbox" trap).
func AssessReadiness(tiers []Tier, claim string) Readiness {
	sandboxProves := false
	operatorProves := false
	for _, t := range tiers {
		if !slices.Contains(t.Proves, claim) {
			continue
		}
		switch t.Scope {
		case TierSandbox:
			sandboxProves = true
		case TierOperatorCI:
			operatorProves = true
		}
	}
	switch {
	case sandboxProves:
		return ReadinessReady
	case operatorProves:
		return ReadinessDeferred
	default:
		return ReadinessPark
	}
}
