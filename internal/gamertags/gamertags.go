// Package gamertags holds the small read helper M09 needs to test a user's
// gamertags against live container rosters. It exists as a leaf package
// (core.App + dbx only) so the three consumers that need it — the WS room
// guard, the screen/VNC proxy, and the play roster resolver — share one
// query + sanitization path without importing each other.
package gamertags

import (
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// UsableStatuses are the gamertag statuses that may grant match / box
// access through the authz rostered_player predicate (A.2): pending and
// blocked tags are excluded. The authz adapter reads this; flip it here.
var UsableStatuses = []string{"approved", "allowed"}

// UsableForUser is SanitizedForUser restricted to UsableStatuses — the tag
// set the authz predicates intersect with live rosters. Returns (nil, nil)
// for an empty userID or a user with no usable tags.
func UsableForUser(app core.App, userID string) ([]string, error) {
	if app == nil || userID == "" || len(UsableStatuses) == 0 {
		return nil, nil
	}
	params := dbx.Params{"userID": userID}
	clauses := make([]string, 0, len(UsableStatuses))
	for i, status := range UsableStatuses {
		key := "status" + strconv.Itoa(i)
		params[key] = status
		clauses = append(clauses, "status = {:"+key+"}")
	}
	rows, err := app.FindRecordsByFilter(
		"gamertags",
		"user = {:userID} && ("+strings.Join(clauses, " || ")+")",
		"", 0, 0,
		params,
	)
	if err != nil {
		return nil, err
	}
	return sanitizedTags(rows), nil
}

// SanitizedForUser returns the lowercased+trimmed gamertag strings the user
// owns, excluding blocked rows (a blocked tag must not grant match access).
// Reads the gamertags.sanitized column maintained by the gamertags_sanitize
// hook, falling back to sanitizing the raw tag if a row predates the hook.
// Returns (nil, nil) for an empty userID or a user with no usable tags.
func SanitizedForUser(app core.App, userID string) ([]string, error) {
	if app == nil || userID == "" {
		return nil, nil
	}
	rows, err := app.FindRecordsByFilter(
		"gamertags",
		"user = {:userID} && status != 'blocked'",
		"", 0, 0,
		dbx.Params{"userID": userID},
	)
	if err != nil {
		return nil, err
	}
	return sanitizedTags(rows), nil
}

// sanitizedTags projects gamertag rows to their sanitized strings, reading
// the gamertags.sanitized column maintained by the gamertags_sanitize hook
// and falling back to lower/trim of the raw tag for rows that predate it.
func sanitizedTags(rows []*core.Record) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		s := r.GetString("sanitized")
		if s == "" {
			s = strings.ToLower(strings.TrimSpace(r.GetString("tag")))
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
