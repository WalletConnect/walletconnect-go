// Package testenv reads the live relay settings for tests.
//
// Without TEST_PROJECT_ID a live test is skipped, so `go test ./...` works
// offline. In CI (CI is set) a missing project ID fails the test instead, so
// a lost secret cannot turn the live suite into silent skips. Workflows set
// TEST_ALLOW_SKIP_LIVE for runs that cannot have the secret, such as pull
// requests from forks.
package testenv

import (
	"os"
	"testing"
)

const defaultRelayURL = "wss://relay.walletconnect.com"

// Relay returns the relay URL and project ID, or skips the test.
func Relay(t testing.TB) (url, projectID string) {
	t.Helper()
	projectID = os.Getenv("TEST_PROJECT_ID")
	if projectID == "" {
		SkipOrFail(t, "TEST_PROJECT_ID is not set")
	}
	url = os.Getenv("TEST_RELAY_URL")
	if url == "" {
		url = defaultRelayURL
	}
	return url, projectID
}

// SkipOrFail skips a test that lacks something it needs, but fails it in CI
// unless TEST_ALLOW_SKIP_LIVE is set.
func SkipOrFail(t testing.TB, reason string) {
	t.Helper()
	if os.Getenv("CI") != "" && os.Getenv("TEST_ALLOW_SKIP_LIVE") == "" {
		t.Fatalf("%s; live tests must run in CI (set TEST_ALLOW_SKIP_LIVE only for runs that cannot have the secret)", reason)
	}
	t.Skipf("%s; skipping live test", reason)
}
