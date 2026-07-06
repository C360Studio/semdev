// Package e2e holds semdev's mock-LLM spine journey — the end-to-end test that
// ties the constitution pins together (issue → change → approval → dev loop →
// measurement → floors → review → clean-room verify → PR). The journey itself
// lands in group 11; this package exists so `task e2e` has a target and the
// mock-ladder discipline (S6) is wired from the start.
package e2e
