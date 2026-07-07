package cleanroom

import "testing"

// The load-bearing carry-forward pin: the classifier MUST distinguish a registry
// unreachable (transport → Retry) from a missing/fabricated coordinate (genuine →
// Fail). verify.Decide is correct only given this contract; it cannot enforce it.
func TestClassifyResolveTransportVsGenuine(t *testing.T) {
	cases := []struct {
		name   string
		result Result
		want   ResolveClass
	}{
		{"clean resolve", Result{ExitCode: 0}, ResolveOK},
		// Transport faults → Retry, never a terminal reject of a good artifact.
		{"dns i/o timeout", Result{ExitCode: 1, Stderr: "go: dial tcp: lookup proxy.golang.org: i/o timeout"}, ResolveTransport},
		{"connection refused", Result{ExitCode: 1, Stderr: "dial tcp 127.0.0.1:443: connect: connection refused"}, ResolveTransport},
		{"tls handshake", Result{ExitCode: 1, Stderr: "net/http: TLS handshake timeout"}, ResolveTransport},
		{"proxy 502", Result{ExitCode: 1, Stderr: "reading https://proxy.golang.org/...: 502 Bad Gateway"}, ResolveTransport},
		{"proxy 429 rate limit", Result{ExitCode: 1, Stderr: "reading https://proxy.golang.org/example.com/@v/list: 429 Too Many Requests"}, ResolveTransport},
		{"proxy 500", Result{ExitCode: 1, Stderr: "reading https://proxy.golang.org/example.com/@v/list: 500 Internal Server Error"}, ResolveTransport},
		{"no such host", Result{ExitCode: 1, Stderr: "dial tcp: lookup goproxy.internal: no such host"}, ResolveTransport},
		// Genuine missing/fabricated coordinates → Fail (fail closed on the artifact).
		{"no matching versions", Result{ExitCode: 1, Stderr: "go: example.com/fake@v9.9.9: no matching versions for query \"v9.9.9\""}, ResolveFailed},
		{"unknown revision", Result{ExitCode: 1, Stderr: "go: example.com/fake@v1.2.3: unknown revision v1.2.3"}, ResolveFailed},
		{"no required module", Result{ExitCode: 1, Stderr: "no required module provides package example.com/fabricated; to add it:"}, ResolveFailed},
		{"404 not found", Result{ExitCode: 1, Stderr: "reading example.com/fake/@v/list: 404 Not Found"}, ResolveFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := ClassifyResolve(tc.result)
			if got != tc.want {
				t.Errorf("ClassifyResolve(%q) = %v (%s), want %v", tc.result.Stderr, got, detail, tc.want)
			}
		})
	}
}

// Fail-closed default: a non-zero resolve exit with NO recognized transport
// signature is treated as a GENUINE failure (Fail), not silently retried — a broken
// artifact must not spin. (An unrecognized fault is recoverable: the human sees the
// terminal fail with the resolve detail.)
func TestClassifyResolveUnrecognizedFailureIsGenuine(t *testing.T) {
	got, _ := ClassifyResolve(Result{ExitCode: 2, Stderr: "go: some novel error phrasing we have never seen"})
	if got != ResolveFailed {
		t.Errorf("unrecognized non-zero resolve = %v, want ResolveFailed (fail closed on the artifact)", got)
	}
}

// A transport signature only reclassifies a FAILED resolve — a zero exit is always
// ResolveOK even if the output happens to mention a timeout in passing.
func TestClassifyResolveZeroExitIsAlwaysOK(t *testing.T) {
	got, _ := ClassifyResolve(Result{ExitCode: 0, Stdout: "downloaded; note: retried once after i/o timeout"})
	if got != ResolveOK {
		t.Errorf("zero-exit resolve = %v, want ResolveOK regardless of output", got)
	}
}
