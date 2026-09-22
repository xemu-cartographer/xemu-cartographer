// Package rostergrace adds a sliding grace window to the M09 roster-membership
// access gate. Editing a gametype (or any transient lobby churn) can briefly
// drop a gamertag from a container's live roster; without leeway that instantly
// revokes the player's screen/VNC + WS access and kicks them off mid-edit. The
// tracker remembers, per (container, gamertag), the last time the gamertag was
// actually seen in the container's roster, and keeps access valid for a TTL
// after presence lapses.
//
// Only a live sighting refreshes the window — grace-granted access does NOT —
// so a genuinely removed gamertag loses access once the TTL elapses with no
// presence (fail-closed). Admins bypass this gate in the callers, before it's
// consulted.
//
// SECURITY NOTE: this is a deliberate, Stewart-requested usability/security
// trade-off (M09). For up to the TTL after a gamertag leaves a container's
// roster, that user can still reach the container's screen/VNC + live WS data.
// Revocation is intentionally not instant. Keep the window short.
package rostergrace

import (
	"sync"
	"time"

	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
)

// DefaultTTL is the grace window — how long access survives after a gamertag
// was last seen in a container's roster. This is the tune point; change it (or
// construct a Tracker with New) to adjust the leeway.
const DefaultTTL = 5 * time.Minute

// Default is the process-wide tracker shared by the screen/VNC proxy and the WS
// join_room gate, so a live sighting on either surface keeps both alive.
var Default = New(DefaultTTL)

// Tracker holds per-(container, gamertag) last-seen timestamps and grants
// access within a sliding TTL of the last live sighting. Safe for concurrent
// use (each access check re-evaluates under the lock).
type Tracker struct {
	ttl  time.Duration
	mu   sync.Mutex
	seen map[string]time.Time
}

// New returns a Tracker with the given grace window.
func New(ttl time.Duration) *Tracker {
	return &Tracker{ttl: ttl, seen: map[string]time.Time{}}
}

func key(container, tag string) string { return container + "\x00" + tag }

// Allow reports whether a caller owning sanitizedTags may access container,
// given the current membership view + the grace window:
//
//   - a tag present in the roster now refreshes its window and grants access;
//   - a tag absent now grants access only if it was last seen within the TTL.
//
// Grace-granted access never refreshes the window, so a removed gamertag's
// access lapses once the TTL passes with no live presence. `now` is injected so
// callers pass time.Now() and tests can advance time deterministically. Empty
// tags deny; expired entries are pruned on inspection.
func (t *Tracker) Allow(view []scraperiface.ContainerMembership, container string, sanitizedTags []string, now time.Time) bool {
	if len(sanitizedTags) == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	allowed := false
	for _, tag := range sanitizedTags {
		if tag == "" {
			continue
		}
		k := key(container, tag)
		if scraperiface.ContainerHasGamertag(view, container, []string{tag}) {
			t.seen[k] = now // live sighting refreshes the window
			allowed = true
			continue
		}
		// Absent now — grant only within the grace window of the last sighting.
		if last, ok := t.seen[k]; ok {
			if now.Sub(last) <= t.ttl {
				allowed = true
			} else {
				delete(t.seen, k) // expired — clean up
			}
		}
	}
	return allowed
}
