// Package e2e holds semdev's mock-LLM bridge proof — the end-to-end test that
// ties the constitution pins together (issue → change → approval → dev loop →
// measurement → floors → review → clean-room verify → PR). It boots the REAL
// shared runtime against an in-process mock LLM (zero paid tokens) and drives
// the whole issue→PR arc through it.
//
// It is a BRIDGE PROOF, not a completeness claim (G7/G10): it proves the rail
// CONNECTS end-to-end under the mock — that every station's rules, tools, and
// facts wire together — not that the rail is correct, production-shaped, or
// that M0 is complete. Real-LLM evidence and a real forge delivery are what a
// completion claim requires (see internal/ledger: a mock run is bridge proof
// and never counts as real-LLM evidence).
package e2e
