package scraper

import "github.com/xemu-cartographer/xc-scraper/runner"

// ContainerMembership is the per-container identity view M09 uses to route a
// logged-in player to the box's screen for the match they're in. Alias of
// runner.ContainerMembership (moved in step 7 part 3c with the match
// helpers below; type identity preserved).
type ContainerMembership = runner.ContainerMembership

// Membership exposes the cross-instance roster identity view. The per-user
// play ownership/roster resolver, the WS room guard, and the screen/VNC
// proxy all read it to decide whether a given user belongs to a given
// container.
type Membership interface {
	Membership() []ContainerMembership
}

// SanitizeIdentity normalizes a name (console / machine / player / gamertag)
// for case-insensitive matching. Mirrors the gamertags_sanitize hook so the
// container-side identities line up with the gamertags.sanitized column.
var SanitizeIdentity = runner.SanitizeIdentity

// MatchContainer returns the first container (deterministic order — callers
// pass a sorted view) whose identity set contains any of the caller's
// gamertags. ok is false when no container matches (idle / not in a match).
var MatchContainer = runner.MatchContainer

// ContainerHasGamertag reports whether the named container's identity set
// contains any of the caller's gamertags. Used by the WS room guard and the
// screen/VNC proxy to authorize access to one specific container.
var ContainerHasGamertag = runner.ContainerHasGamertag
