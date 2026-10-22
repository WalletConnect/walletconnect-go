// Package testenv reads the live relay settings for tests. Tests that need
// the relay skip when TEST_PROJECT_ID is unset.
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
		t.Skip("TEST_PROJECT_ID is not set; skipping live relay test")
	}
	url = os.Getenv("TEST_RELAY_URL")
	if url == "" {
		url = defaultRelayURL
	}
	return url, projectID
}
