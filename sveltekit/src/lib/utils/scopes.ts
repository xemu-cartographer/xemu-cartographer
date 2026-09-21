/**
 * Scope matching — a TypeScript mirror of `internal/authz/scope.go`.
 *
 * The frontend only uses this to *hide* UI the server would reject anyway
 * (design §9); every real decision is made server-side. Keep the semantics
 * identical to the Go side so a hidden button never hides something the
 * server would allow, and vice versa. `scopes.test.ts` pins the §3.2 table.
 *
 * Grammar (design §3.1):
 *
 *   scope      := action-pat [ ":" selector ]
 *   action-pat := "*" | action | family ".*"     family = dotted prefix of an action
 *   selector   := "*" | segment { ":" segment }  "*" alone = match anything
 *   segment    := "*" | 1*( a-z / 0-9 / "-" / "_" / "." / " " )
 *
 * - Lower-case ASCII only; the scope side is strict, the want side is
 *   canonicalised (trim, lower-case, collapse whitespace) before comparison.
 * - "*" as the whole trailing selector matches every selector including the
 *   empty one; a "*" segment matches exactly one segment (counts must match).
 * - An exact action with no selector matches only the empty selector; a
 *   family pattern or bare "*" with no selector matches every selector.
 * - "*" inside a want is a literal, never a wildcard.
 */

/** The closed action vocabulary (`internal/authz/action.go`). */
export const ACTIONS = [
	// WS
	'room.join',
	'ws.send',
	'scraper.probe',
	'scraper.events',
	'scraper.state',
	// Overlay
	'overlay.read_state',
	'overlay.list_consoles',
	'overlay.mint',
	// Box: screen (view/drive) + lifecycle
	'box.view',
	'box.drive',
	'box.read',
	'box.control',
	'box.teardown',
	'box.provision',
	'box.provision_named',
	'container.manage',
	// Admin route groups
	'admin.admin',
	'admin.users',
	'admin.containers',
	'admin.scraper',
	'admin.xemu',
	'admin.pod',
	// Library / ISO
	'library.manage',
	'iso.set_policy',
	// Roles / moderation
	'role.grant',
	'role.revoke',
	'user.moderate',
	'user.soft_delete',
	'gamertag.moderate',
	'team.moderate',
	'notification.admin_edit',
	// Teams (non-admin)
	'team.invite',
	'team.remove',
	'team.decide',
	// LAN
	'lan.saves.identity',
	'lan.saves.file',
	'lan.saves.build',
	'lan.saves.download',
	'lan.saves.manifest',
	'lan.saves.meta',
	'lan.sync.manifest',
	'lan.sync.download_game',
	'lan.sync.download_app',
	// Tokens
	'token.mint',
	'token.revoke',
	'token.list',
	// Discord
	'discord.config',
	'discord.bind_channel',
	'discord.stats.read',
	'discord.box',
	'discord.post'
] as const;

export type Action = (typeof ACTIONS)[number];

const actionSet: ReadonlySet<string> = new Set<string>(ACTIONS);

const WILDCARD = '*';

/** A parsed scope pattern (`authz.ScopePat`). */
export interface ScopePat {
	/** Exact action, or the family for family patterns; '' when `any`. */
	action: string;
	/** action-pat was `<family>.*`. */
	family: boolean;
	/** Selector segments; null when the scope has no selector. */
	selector: string[] | null;
	/** The bare `*` scope: every action, every selector. */
	any: boolean;
}

/** isKnownAction reports whether `a` is one of the closed vocabulary. */
export function isKnownAction(a: string): a is Action {
	return actionSet.has(a);
}

/**
 * isActionFamily reports whether `family` is a dotted prefix of at least one
 * known action, i.e. `<family>.*` is a meaningful pattern ("lan", "lan.saves",
 * "room", ...).
 */
export function isActionFamily(family: string): boolean {
	if (family === '') return false;
	const prefix = family + '.';
	return ACTIONS.some((a) => a.startsWith(prefix));
}

/** collapseWs mirrors `strings.Join(strings.Fields(s), " ")`. */
function collapseWs(s: string): string {
	return s.split(/\s+/).filter(Boolean).join(' ');
}

/**
 * canonScope canonicalises a scope string: trim, lower-case, and collapse
 * internal whitespace inside each ":"-separated segment. It never validates —
 * `parseScope` does — so canonScope(bad) is still bad.
 */
export function canonScope(s: string): string {
	s = s.trim().toLowerCase();
	if (s === '') return '';
	return s
		.split(':')
		.map((seg) => collapseWs(seg))
		.join(':');
}

/** validateSegment enforces the segment production of the grammar. */
function validateSegment(seg: string): boolean {
	if (seg === '') return false;
	if (seg === WILDCARD) return true;
	return /^[a-z0-9\-_. ]+$/.test(seg);
}

/**
 * parseScope parses a scope pattern; null on any syntax error (the Go side's
 * ErrScopeSyntax): empty, leading/trailing ':', an empty segment, "*" that is
 * not a whole segment, a character outside the grammar, upper-case anywhere,
 * or an action-pat that is neither a known action, `<family>.*`, nor "*".
 */
export function parseScope(s: string): ScopePat | null {
	if (s === '') return null;
	if (s.toLowerCase() !== s) return null;
	if (s.startsWith(':') || s.endsWith(':')) return null;

	const cut = s.indexOf(':');
	const actionPat = cut < 0 ? s : s.slice(0, cut);
	const hasSelector = cut >= 0;
	const selector = hasSelector ? s.slice(cut + 1) : '';

	const pat: ScopePat = { action: '', family: false, selector: null, any: false };
	if (actionPat === WILDCARD) {
		if (hasSelector) return null;
		pat.any = true;
	} else if (actionPat.endsWith('.*')) {
		const family = actionPat.slice(0, -2);
		if (!isActionFamily(family)) return null;
		pat.action = family;
		pat.family = true;
	} else {
		if (!isKnownAction(actionPat)) return null;
		pat.action = actionPat;
	}

	if (!hasSelector) return pat;
	const segs = selector.split(':');
	for (const seg of segs) {
		if (!validateSegment(seg)) return null;
	}
	pat.selector = segs;
	return pat;
}

/**
 * parseWant splits a want ("<action>" or "<action>:<selector>") into its
 * action and canonicalised selector segments; null when the action is
 * unknown. Want selectors are resource identifiers (LAN file ids contain "/",
 * instance names have no charset rule) so they are only canonicalised, never
 * validated, and "*" in a want is a literal.
 */
function parseWant(want: string): { action: string; selector: string[] | null } | null {
	const cut = want.indexOf(':');
	const head = cut < 0 ? want : want.slice(0, cut);
	if (!isKnownAction(head)) return null;
	if (cut < 0) return { action: head, selector: null };
	const selector = want
		.slice(cut + 1)
		.split(':')
		.map((seg) => collapseWs(seg.toLowerCase()));
	return { action: head, selector };
}

function matchAction(pat: ScopePat, action: string): boolean {
	if (pat.any) return true;
	if (pat.family) return action.startsWith(pat.action + '.');
	return action === pat.action;
}

function matchSelector(pat: ScopePat, want: string[] | null): boolean {
	if (!pat.selector || pat.selector.length === 0) {
		// No selector: exact actions cover only the empty selector; family /
		// bare-"*" patterns cover every selector.
		return pat.family || pat.any || !want || want.length === 0;
	}
	if (pat.selector.length === 1 && pat.selector[0] === WILDCARD) return true;
	const w = want ?? [];
	if (pat.selector.length !== w.length) return false;
	for (let i = 0; i < pat.selector.length; i++) {
		const seg = pat.selector[i];
		if (seg !== WILDCARD && seg !== w[i]) return false;
	}
	return true;
}

/**
 * matchScope reports whether `scope` (a pattern) grants `want` ("<action>" or
 * "<action>:<selector>"). False on any parse error of either side.
 */
export function matchScope(scope: string, want: string): boolean {
	const pat = parseScope(scope);
	if (!pat) return false;
	const w = parseWant(want);
	if (!w) return false;
	return matchAction(pat, w.action) && matchSelector(pat, w.selector);
}

/**
 * hasScope reports whether any pattern in `scopes` grants `want`
 * (`authz.MatchAny`). An empty or missing list never matches.
 */
export function hasScope(scopes: readonly string[] | null | undefined, want: string): boolean {
	if (!scopes) return false;
	for (const s of scopes) {
		if (matchScope(s, want)) return true;
	}
	return false;
}
