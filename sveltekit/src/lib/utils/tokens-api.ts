// Client for the opaque-key routes (/api/admin/tokens — design §6.3).
//
// Three kinds of key: `machine` (LAN clients), `spectator` (overlay browser
// sources, minted from Studio) and `device` (stations). The secret is returned
// exactly once by mint; the list never carries it. Every call sends the PB
// JWT; the server answers 401 to anonymous callers and 403 when the caller
// lacks `token.mint:<kind>` / `token.list` / `token.revoke:<kid>`
// (`overlay.mint` for spectator keys).
import { auth } from '$lib/stores/auth.svelte';
import { apiBaseURL } from '$lib/utils/api-base';

export type TokenKind = 'machine' | 'spectator' | 'device';

export const TOKEN_KINDS: readonly TokenKind[] = ['machine', 'spectator', 'device'];

export class TokensApiError extends Error {
	status: number;
	constructor(status: number, message: string) {
		super(message);
		this.status = status;
		this.name = 'TokensApiError';
	}
}

/** POST /api/admin/tokens body. `expires_at` is RFC3339 or omitted (server
 *  default: spectator now+90d, others never). */
export interface MintRequest {
	kind: TokenKind;
	label: string;
	scopes: string[];
	user?: string;
	container?: string;
	station_id?: string;
	gamertags?: string[];
	expires_at?: string;
}

/** 201 payload — `token` is the `<kid>.<secret>` credential, shown once. */
export interface MintResult {
	kid: string;
	token: string;
	kind: TokenKind;
	label: string;
	scopes: string[];
	expires_at: string;
}

/** One row of GET /api/admin/tokens. Times are RFC3339 UTC or "". */
export interface TokenRow {
	kid: string;
	kind: TokenKind;
	label: string;
	scopes: string[];
	user: string;
	container: string;
	station_id: string;
	gamertags: string[];
	expires_at: string;
	revoked: boolean;
	revoked_at: string;
	last_used_at: string;
	minted_by: string;
	created: string;
}

async function request(method: string, path: string, body?: unknown): Promise<Response> {
	if (!auth.token) {
		throw new TokensApiError(401, 'not authenticated');
	}
	const headers: Record<string, string> = { Authorization: auth.token };
	if (body !== undefined) headers['Content-Type'] = 'application/json';
	const res = await fetch(`${apiBaseURL()}/api/admin/tokens${path}`, {
		method,
		headers,
		body: body !== undefined ? JSON.stringify(body) : undefined
	});
	if (!res.ok) {
		let msg = `HTTP ${res.status}`;
		try {
			// Handlers answer {error}; PocketBase's apis.New*Error answers
			// {message}. Read either.
			const data = await res.clone().json();
			if (data && typeof data === 'object') {
				if ('error' in data && typeof data.error === 'string') msg = data.error;
				else if ('message' in data && typeof data.message === 'string') msg = data.message;
			}
		} catch {
			// non-JSON body; keep default
		}
		throw new TokensApiError(res.status, msg);
	}
	return res;
}

/** POST /api/admin/tokens — mint a key; the returned `token` is shown once. */
export async function mintToken(req: MintRequest): Promise<MintResult> {
	const body: Record<string, unknown> = {
		kind: req.kind,
		label: req.label,
		scopes: req.scopes
	};
	if (req.user) body.user = req.user;
	if (req.container) body.container = req.container;
	if (req.station_id) body.station_id = req.station_id;
	if (req.gamertags && req.gamertags.length) body.gamertags = req.gamertags;
	if (req.expires_at) body.expires_at = req.expires_at;
	const res = await request('POST', '', body);
	return (await res.json()) as MintResult;
}

/** stringList coerces a JSON field that may arrive as null (Go encodes a nil
 *  slice — e.g. a row with no gamertags — as `null`) into a string array. */
function stringList(v: unknown): string[] {
	return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : [];
}

/** GET /api/admin/tokens?kind= — list keys (never the secret). */
export async function listTokens(kind?: TokenKind | ''): Promise<TokenRow[]> {
	const q = kind ? `?kind=${encodeURIComponent(kind)}` : '';
	const res = await request('GET', q);
	const data = (await res.json()) as { tokens?: unknown };
	if (!Array.isArray(data.tokens)) return [];
	return data.tokens
		.filter((r): r is TokenRow => !!r && typeof r === 'object' && typeof r.kid === 'string')
		.map((r) => ({
			...r,
			label: typeof r.label === 'string' ? r.label : '',
			container: typeof r.container === 'string' ? r.container : '',
			station_id: typeof r.station_id === 'string' ? r.station_id : '',
			scopes: stringList(r.scopes),
			gamertags: stringList(r.gamertags)
		}));
}

/** DELETE /api/admin/tokens/{kid} — revoke. 404 unknown kid; 409 for the
 *  legacy LAN_SAVES_TOKEN env key (unset the variable instead). */
export async function revokeToken(kid: string, reason?: string): Promise<void> {
	const body = reason && reason.trim() ? { reason: reason.trim() } : undefined;
	await request('DELETE', `/${encodeURIComponent(kid)}`, body);
}

/**
 * spectatorScopesFor is the per-instance spectator grant Studio mints for an
 * overlay browser source (design §9): read the instance's state plus the
 * four `host:<inst>` room classes the overlays subscribe to.
 */
export function spectatorScopesFor(instance: string): string[] {
	const inst = instance.trim().toLowerCase();
	return [
		`overlay.read_state:${inst}`,
		`room.join:host:${inst}:game_filtered`,
		`room.join:host:${inst}:tick`,
		`room.join:host:${inst}:scenario`,
		`room.join:host:${inst}:event_filtered`
	];
}
