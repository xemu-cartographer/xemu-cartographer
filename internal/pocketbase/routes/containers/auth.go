package containers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
)

// screenTokenCookie is the cookie name used to carry a credential through the
// iframe's sub-resource requests. The iframe entry-point is fetched with
// ?token=…; once validated, this cookie is set with Path scoped to the
// per-container screen prefix so CSS/JS/images/websockify under that prefix
// authenticate without the parent page rewriting URLs.
const screenTokenCookie = "screen_token"

// screenCookieMaxAge bounds the screen cookie's lifetime (12h, PD-11) so a
// forgotten screen tab does not keep a credential alive indefinitely.
const screenCookieMaxAge = 43200

// authorizeBoxAccess admits a caller to the screen/VNC proxy for container
// `name` (M09 9b/9c) for action a — box.view for the noVNC proxy,
// box.drive for the VNC relay. The credential comes from ?token= or the
// screen_token cookie (pb.ResolveScreen: a PB JWT or an opaque device key) and
// the decision is the rule table's: a scoped principal, a user whose
// gamertag is in that container's live roster (with the rostergrace TTL) or
// who owns the box, or a device key bound to the instance. Re-checked on
// every request; fails closed on any lookup error or before the deps are
// installed at boot.
func authorizeBoxAccess(e *core.RequestEvent, name string, a authz.Action) bool {
	if e == nil || e.App == nil {
		return false
	}
	d := pb.Default()
	p, _ := pb.ResolveScreen(e.App, d, e)
	return authz.Can(d, p, a, authz.Container(name))
}

// requireManage is the per-handler container.manage check every mutating
// route (create / start / stop / remove / files / cleanup) runs after the
// group's admin.containers gate: the caller's scopes must cover the named
// container (or the global selector for cleanup). Returns the apis 401/403
// error to bubble, nil when allowed.
func requireManage(e *core.RequestEvent, r authz.Resource) error {
	return pb.Check(pb.Default(), e, authz.ActionContainerManage, r)
}

// setScreenTokenCookie persists the validated ?token= as an HttpOnly cookie
// scoped to the per-container screen prefix, so the iframe's sub-resource
// requests authenticate without anyone rewriting URLs. Path scoping means the
// cookie isn't sent to unrelated PB endpoints; Secure is set whenever the
// request arrived over TLS (directly or via a proxy's X-Forwarded-Proto) and
// the cookie expires after screenCookieMaxAge (PD-11).
func setScreenTokenCookie(e *core.RequestEvent, path, token string) {
	http.SetCookie(e.Response, screenCookie(e.Request, path, token))
}

// screenCookie builds the screen_token cookie for req (split out so the Secure
// / MaxAge attributes are unit-testable without a RequestEvent).
func screenCookie(req *http.Request, path, token string) *http.Cookie {
	return &http.Cookie{
		Name:     screenTokenCookie,
		Value:    token,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsTLS(req),
		MaxAge:   screenCookieMaxAge,
	}
}

// requestIsTLS reports whether req arrived over HTTPS — a direct TLS
// connection or a reverse proxy declaring X-Forwarded-Proto: https.
func requestIsTLS(req *http.Request) bool {
	if req == nil {
		return false
	}
	return req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https"
}
