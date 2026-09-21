package play

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/gamertags"
	"github.com/xemu-cartographer/xemu-cartographer/internal/instancename"
	"github.com/xemu-cartographer/xemu-cartographer/internal/isoingest"
	"github.com/xemu-cartographer/xemu-cartographer/internal/lansync"
)

func init() {
	register(registerISOs)
	register(registerISOMaps)
	register(registerRequest)
}

// isoOption is the player-safe projection of an `isos` catalog row. The raw
// library `filename` is deliberately omitted — the player picks by id, and the
// server resolves the on-disk file when provisioning.
type isoOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TitleID     string `json:"title_id"`
	Description string `json:"description"`
}

// GET /api/play/isos — the games a player may request an instance for: every
// play-role entry in the ISO library, sorted by name. RequireAuth only
// (group-bound) — any authed player may browse the catalog to pick from; no
// container scoping applies until they actually request one.
func registerISOs() {
	Group.GET("/isos", func(e *core.RequestEvent) error {
		records, err := e.App.FindRecordsByFilter("isos", "role = 'play'", "name", 0, 0, dbx.Params{})
		if err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		out := make([]isoOption, 0, len(records))
		for _, r := range records {
			out = append(out, isoOption{
				ID:          r.Id,
				Name:        r.GetString("name"),
				TitleID:     r.GetString("title_id"),
				Description: r.GetString("description"),
			})
		}
		return e.JSON(http.StatusOK, out)
	})
}

// GET /api/play/isos/{id}/maps — the maps (name / type / thumbnail) a chosen
// game ships, so the catalog can surface what a build actually contains. Read
// the build's iso row check is skipped here (map rows are harmless to list);
// RequireAuth (group-bound) is the gate.
func registerISOMaps() {
	Group.GET("/isos/{id}/maps", func(e *core.RequestEvent) error {
		return e.JSON(http.StatusOK, isoingest.MapsForISO(e.App, e.Request.PathValue("id")))
	})
}

// requestResponse is the payload returned when a player provisions an instance.
type requestResponse struct {
	Instance string `json:"instance"`
	Index    int    `json:"index"`
	ISO      string `json:"iso"`      // the chosen game's display name
	TitleID  string `json:"title_id"` // its title id (may be "")
	GameISO  string `json:"game_iso"` // resolved host path of the attached disc
}

// POST /api/play/request — the player picks a game and gets a box: resolve the
// chosen library ISO, provision + start a fresh xemu instance booting straight
// into it (ADR-0004), and return the new instance. Body:
//
//	{"iso": "<isos record id>", "name": "<optional, box.provision_named only>"}
//
// This is the last player-flow piece that was admin-only (routes/containers) —
// it's now self-serve, but narrowly: a caller without box.provision_named on
// the ISO always gets a single stable per-user box name ("play-<uid>"), so a
// player can neither name arbitrary containers nor field more than one. A
// scoped admin may pass an explicit name. Fails closed with 409 when that box
// already exists (tear it down first), 503 when provisioning isn't wired
// (CONTAINERS_ENABLED=false), 403 when the chosen ISO isn't player-available
// or box.provision on it is denied. The admin screen/VNC path is untouched.
func registerRequest() {
	Group.POST("/request", func(e *core.RequestEvent) error {
		var body struct {
			ISO  string `json:"iso"`
			Name string `json:"name"`
		}
		if err := e.BindBody(&body); err != nil {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if strings.TrimSpace(body.ISO) == "" {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": "iso is required"})
		}

		// Resolve + validate the chosen game BEFORE the provisioning-capability
		// check, so a bad request gets a precise 400/404/403 regardless of whether
		// provisioning happens to be wired. Players may only launch play-role
		// entries; an organizer re-roling one (or a drift flag shelving it) hides
		// it here too.
		rec, err := e.App.FindRecordById("isos", strings.TrimSpace(body.ISO))
		if err != nil {
			return e.JSON(http.StatusNotFound, map[string]string{"error": "game not found in the library"})
		}
		// box.provision on the chosen ISO (R-9): any linked user may field a
		// box for a disc; a banned / non-user principal is refused.
		d := pb.Default()
		if err := pb.Check(d, e, authz.ActionBoxProvision, authz.ISO(rec.Id)); err != nil {
			return err
		}
		if rec.GetString("role") != "play" {
			return e.JSON(http.StatusForbidden, map[string]string{"error": "that game is not available to play right now"})
		}
		// A xemu-cart HOST instance boots the game's dedicated SERVER build when
		// it has one, else the game itself — resolved to the managed <id>.iso and
		// integrity-checked (drift) before boot so bad bytes never run. (The
		// companion-app LAN-sync path serves the game build to real Xboxes.)
		bootFilename, err := resolveBootISO(e.App, lansync.Load(), rec)
		if err != nil {
			return e.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		}

		// Provisioning subsystem must be wired (CONTAINERS_ENABLED) to go further.
		if Provisioner == nil {
			return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "instance provisioning not enabled"})
		}

		// An explicit pretty name needs box.provision_named on the ISO (a
		// scoped admin); everyone else gets the per-user box.
		named := authz.Can(d, pb.Get(e), authz.ActionBoxProvisionNamed, authz.ISO(rec.Id))
		display, name := instanceName(Provisioner.NamePrefix(), e.Auth.Id, body.Name, named)
		if name == "" {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": "could not determine an instance name"})
		}
		// Name the box after its OWNER: when no explicit display name was given
		// (the normal /play path derives an empty one), stamp the owner's primary
		// gamertag as the Xbox console nickname so the box is identifiable in the
		// System Link lobby and the logs instead of the opaque "play-<uid>" slug.
		// The CONTAINER name stays "<prefix>play-<uid>" — that's the stable
		// ownership key (one box per user, the reaper + host-drive scoping marker);
		// only the user-visible console name changes. Falls back to the container
		// name (unchanged behavior) when the owner has no gamertag.
		if display == "" {
			if tags, err := gamertags.SanitizedForUser(e.App, e.Auth.Id); err == nil && len(tags) > 0 {
				display = instancename.Display(tags[0])
			}
		}
		if Provisioner.Exists(name) {
			return e.JSON(http.StatusConflict, map[string]string{
				"error": "you already have an instance (" + name + "); tear it down first",
			})
		}

		res, err := Provisioner.Provision(name, bootFilename, display)
		if err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return e.JSON(http.StatusCreated, requestResponse{
			Instance: res.Name,
			Index:    res.Index,
			ISO:      rec.GetString("name"),
			TitleID:  rec.GetString("title_id"),
			GameISO:  res.GameISO,
		})
	})
}

// resolveBootISO picks the managed disc a fresh HOST instance should boot for
// the chosen game — the linked SERVER build (server_iso) when set + resolvable,
// else the game itself — verifies its bytes against the content-hash anchor
// (refusing on drift), and returns the managed basename (<id>.iso) podman
// resolves against the ISO library. A missing/dangling server_iso falls back to
// the game, so deleting a server build never bricks the games that referenced
// it. The pure pick is bootRecordID (unit-tested); the DB + drift touch is here.
func resolveBootISO(app core.App, cfg lansync.Config, game *core.Record) (string, error) {
	bootRec := game
	if id := game.GetString("server_iso"); id != "" {
		if s, err := app.FindRecordById("isos", id); err == nil {
			bootRec = s
		}
	}
	if !isoingest.VerifyAndFlag(app, cfg, bootRec) {
		return "", fmt.Errorf("disc %q failed its integrity check (drift) — refusing to boot", bootRec.GetString("name"))
	}
	return lansync.ManagedISOName(bootRec.Id), nil
}

// bootRecordID is the pure server/game boot decision: boot the SERVER build's
// record when the game links one, else the game's own record. (Stewart's model —
// the neutral-host / host-doesn't-spawn flag is deliberately deferred.)
func bootRecordID(gameID, serverID string) string {
	if strings.TrimSpace(serverID) != "" {
		return serverID
	}
	return gameID
}

// instanceName is the pure DECOUPLED-naming decision for request-instance. It
// returns the canonical pretty display name AND the derived podman container
// name:
//
//   - Without named (no box.provision_named on the ISO) the caller ALWAYS gets
//     a single stable per-user box "play-<uid>" (so a player can't name
//     arbitrary containers or field more than one — the Exists check then
//     fails a second request closed) with an EMPTY display name (the console
//     name falls back to the container name).
//   - With named the caller may pass an explicit pretty name: it's validated
//     as a display name (printable ASCII, ≤15) and the container name is its
//     slug. If the pretty name slugs to empty (all punctuation), the display
//     is kept but the container falls back to the per-user "play-<uid>" scheme
//     for uniqueness.
//   - prefix (the deployment's container-name namespace, empty in prod) is
//     prepended to the container name so a beta sharing the host podman daemon
//     gives "beta-*" boxes that can't collide with prod's. It is NOT applied to
//     the display name (that's the pretty canonical, unmangled).
//
// container is "" only when there's nothing to derive from (no userID and no
// usable override), so the caller still rejects it. Split out so it's
// unit-testable with no request/podman.
func instanceName(prefix, userID, override string, named bool) (display, container string) {
	uid := sanitizeName(userID)
	perUser := ""
	if uid != "" {
		perUser = prefix + "play-" + uid
	}
	if named {
		if d := instancename.Display(override); d != "" {
			if slug := instancename.Slug(d); slug != "" {
				return d, prefix + slug
			}
			// Pretty name present but slugs to empty — keep the display, fall
			// back to the uid scheme for a valid, unique container name.
			return d, perUser
		}
	}
	// Not named, or named with no usable override: uid-based box, no pretty name.
	return "", perUser
}

// sanitizeName lowercases and keeps only podman-safe name characters
// ([a-z0-9_.-]), collapsing everything else away. PB record ids are already
// lowercase alphanumeric, so the derived per-user name passes through intact;
// this only guards an admin's free-form override.
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}
