package middleware

import "github.com/google/uuid"

// userIDNamespace is an arbitrary but frozen UUID. Its only job is to keep
// stableUserID's output in a namespace nothing else derives ids in; changing
// it would re-map every existing user onto a new id, so it must never change.
var userIDNamespace = uuid.MustParse("88bd7d7f-69d8-420d-8525-7380806ec09f")

// stableUserID maps an identity provider's subject onto a UUID.
//
// The read models declare user_id as a Postgres `uuid` column. That was safe
// only because a Cognito `sub` happens to be a UUID — Auth0 issues
// `auth0|68f3...` and Google a 21-digit number, and neither parses. Without
// this the projector would fail to insert on every event carrying a user, and
// it would fail at the read side rather than at the door, which is the worst
// place to discover it.
//
// UUIDv5 is a hash rather than an allocation: the same subject always yields
// the same id, so there is no mapping table to keep, nothing to migrate, and a
// replay derives exactly the ids it derived the first time — the same property
// projections already rely on for row ids.
//
// The issuer is part of the hashed value because a `sub` is only unique within
// the issuer that minted it. The cost is that moving to a different issuer —
// a new Auth0 tenant, say — gives the same human a new id and orphans their
// data. That is the correct default for a claim that is only locally unique,
// but it is the thing to remember before switching tenants casually.
//
// A subject that is already a UUID passes through untouched, which keeps the
// local dev user and any existing Cognito-shaped id exactly as they are.
func stableUserID(issuer, sub string) string {
	if parsed, err := uuid.Parse(sub); err == nil {
		return parsed.String()
	}
	return uuid.NewSHA1(userIDNamespace, []byte(issuer+"\n"+sub)).String()
}
