package middleware

import (
	"testing"

	"github.com/google/uuid"
)

// The shapes real providers actually issue. Auth0 and Google are the reason
// this mapping exists: neither `sub` is a UUID, and the read models store
// user_id as one.
func TestStableUserIDAlwaysProducesAUUID(t *testing.T) {
	subjects := map[string]string{
		"auth0":   "auth0|68f3a1b2c4d5e6f7a8b9c0d1",
		"google":  "117482938475610293847",
		"cognito": "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"dev":     "00000000-0000-0000-0000-000000000001",
		"opaque":  "not-a-uuid-at-all",
	}

	for name, sub := range subjects {
		t.Run(name, func(t *testing.T) {
			got := stableUserID("https://example.eu.auth0.com/", sub)
			if _, err := uuid.Parse(got); err != nil {
				t.Fatalf("stableUserID(%q) = %q, which is not a UUID: %v", sub, got, err)
			}
		})
	}
}

// Determinism is what lets a replay rebuild the same rows. If this ever fails,
// every projection keyed by user_id silently splits in two.
func TestStableUserIDIsDeterministic(t *testing.T) {
	const iss, sub = "https://example.eu.auth0.com/", "auth0|68f3a1b2c4d5e6f7a8b9c0d1"
	first := stableUserID(iss, sub)
	for i := 0; i < 100; i++ {
		if got := stableUserID(iss, sub); got != first {
			t.Fatalf("call %d returned %q, want %q", i, got, first)
		}
	}
}

// A subject that is already a UUID must survive untouched, or switching the
// local dev user onto this path would orphan every wallet it ever created.
func TestStableUserIDPassesUUIDSubjectsThrough(t *testing.T) {
	const dev = "00000000-0000-0000-0000-000000000001"
	if got := stableUserID("", dev); got != dev {
		t.Fatalf("stableUserID(%q) = %q, want it unchanged", dev, got)
	}
}

// A `sub` is only unique within its issuer, so the same subject from two
// providers must not collide onto one account.
func TestStableUserIDSeparatesIssuers(t *testing.T) {
	const sub = "117482938475610293847"
	a := stableUserID("https://accounts.google.com", sub)
	b := stableUserID("https://example.eu.auth0.com/", sub)
	if a == b {
		t.Fatalf("subject %q collided across issuers: both mapped to %s", sub, a)
	}
}
