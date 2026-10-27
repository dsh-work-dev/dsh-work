package release

import "golang.org/x/mod/semver"

// Compare returns -1, 0 or +1 for a < b, a == b and a > b under SemVer 2.0.0
// precedence. Build metadata never affects precedence.
//
// The comparison is the specification's own: identifiers are compared left to
// right, numeric identifiers compare numerically, alphanumeric identifiers
// compare in ASCII order, a numeric identifier has lower precedence than an
// alphanumeric one, a larger set of pre-release fields wins when every
// preceding field is equal, and a version without a pre-release part has
// higher precedence than one with it.
//
// Precedence does not order the release channels. For one base version SemVer
// places nightly above beta, because the identifiers compare in ASCII order
// and "beta" < "nightly"; a nightly build therefore looks newer than a beta
// build of the same base. Channel membership always comes from Channel, never
// from Compare. See Version.Channel and Channels.
//
// x/mod/semver is used for the comparison instead of a hand-rolled comparator:
// it is maintained by the Go team, is already in the module graph, and its
// precedence rules are exactly the specification's.
func Compare(a, b Version) int {
	if a.IsZero() || b.IsZero() {
		// A comparison needs parsed versions on both sides; an unparsed value
		// carries no precedence at all.
		switch {
		case a.IsZero() && b.IsZero():
			return 0
		case a.IsZero():
			return -1
		default:
			return 1
		}
	}
	return semver.Compare(a.canonical(), b.canonical())
}

// IsNewer reports whether candidate is strictly newer than current under SemVer
// precedence. Both versions must belong to the same channel for this answer to
// be meaningful; see Compare.
func IsNewer(candidate, current Version) bool {
	return Compare(candidate, current) > 0
}

// Equal reports whether the two versions have the same precedence.
func Equal(a, b Version) bool { return Compare(a, b) == 0 }

// SameBase reports whether two versions share the same X.Y.Z base, which is the
// condition a stable release and its beta releases must satisfy.
func SameBase(a, b Version) bool { return !a.IsZero() && !b.IsZero() && a.Base() == b.Base() }

// BaseSupersedes reports whether base is strictly higher than previous at the
// base-version level. A nightly build uses it to decide whether the config base
// is already past the last published stable base or must move to the next
// patch.
func BaseSupersedes(base, previous Version) bool {
	if base.IsZero() || previous.IsZero() {
		return false
	}
	higher, err := Parse(base.Base())
	if err != nil {
		return false
	}
	lower, err := Parse(previous.Base())
	if err != nil {
		return false
	}
	return Compare(higher, lower) > 0
}
