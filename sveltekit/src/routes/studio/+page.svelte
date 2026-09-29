<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import {
		TvIcon,
		CopyIcon,
		RefreshCwIcon,
		LoaderIcon,
		MonitorIcon,
		KeyIcon,
		ShieldOffIcon
	} from '@lucide/svelte';
	import { toaster, describeAsyncError } from '$lib/stores/toaster';
	import { canManageOverlays, listConsoles, type OverlayConsole } from '$lib/utils/overlay-api';
	import { mintToken, spectatorScopesFor, type MintResult } from '$lib/utils/tokens-api';

	// Operator page: overlays target by CONSOLE NAME. authz (design §9): each
	// console card mints a SPECTATOR KEY scoped to the host instance currently
	// showing that console (overlay.read_state + the four host:<inst> room
	// classes the overlays subscribe to) and bakes it into the browser-source
	// URL as ?spectator=<key>&console=<name>. The tokenless ?console= URL is
	// kept as the legacy "window" path: the anonymous door only admits the
	// instance's OWN console name, so it can't follow a lobby peer.
	const authorized = canManageOverlays();
	const origin = $derived(browser ? window.location.origin : '');

	let consoles = $state<OverlayConsole[]>([]);
	let loading = $state(true);
	let names = $state(''); // optional display-name overrides, applied to every URL
	// Spectator keys minted this session, keyed by console name. The secret
	// is only ever returned once by the server, so a reload forgets it —
	// mint again (the old key stays valid until it expires or is revoked
	// from /admin/tokens/).
	let keys = $state<Record<string, MintResult>>({});
	let minting = $state<Record<string, boolean>>({});
	let showLegacy = $state<Record<string, boolean>>({});

	// The overlay surfaces + their suggested OBS Browser Source sizes. Each
	// graphic renders at its natural size at the top-left of the source; the
	// board/report heights grow with the roster, so size generously and let the
	// transparent area fall where it may (or add ?anchor=center to centre it).
	const OVERLAYS = [
		{ id: 'overlay', label: 'POV', path: '/overlay/', size: '1440 × 1080' },
		{ id: 'scorebug', label: 'Scorebug', path: '/scorebug/', size: '700 × 84' },
		{ id: 'leaderboard', label: 'Leaderboard', path: '/leaderboard/', size: '430 × 560 (4v4)' },
		{ id: 'postgame', label: 'Postgame', path: '/postgame/', size: '900 × 700 (4v4)' }
	];

	function namesParam(): string[] {
		return names.trim() ? [`names=${encodeURIComponent(names.trim())}`] : [];
	}

	/** Legacy (window) URL: the anonymous console door, no credential. */
	function legacyURL(console: string, path: string): string {
		const parts = [`console=${encodeURIComponent(console)}`, ...namesParam()];
		return `${origin}${path}?${parts.join('&')}`;
	}

	/** Spectator URL: the minted key first (the server reads ?spectator=
	 * before ?console=), the console name for resolution + display. */
	function spectatorURL(console: string, path: string): string {
		const key = keys[console];
		if (!key) return legacyURL(console, path);
		const parts = [
			`spectator=${encodeURIComponent(key.token)}`,
			`console=${encodeURIComponent(console)}`,
			...namesParam()
		];
		return `${origin}${path}?${parts.join('&')}`;
	}

	async function copy(u: string) {
		try {
			await navigator.clipboard.writeText(u);
			toaster.success({ title: 'Copied', description: 'Browser-source URL on the clipboard.' });
		} catch {
			toaster.error({ title: 'Copy failed', description: u });
		}
	}

	async function mint(c: OverlayConsole) {
		if (minting[c.console]) return;
		minting = { ...minting, [c.console]: true };
		try {
			const result = await mintToken({
				kind: 'spectator',
				label: `studio:${c.console}`,
				scopes: spectatorScopesFor(c.instance)
			});
			keys = { ...keys, [c.console]: result };
			toaster.success({
				title: 'Spectator key minted',
				description: `${result.kid} — scoped to ${c.instance}. Copy the overlay URLs below.`
			});
		} catch (e) {
			toaster.error({ title: 'Mint failed', description: describeAsyncError(e) });
		} finally {
			minting = { ...minting, [c.console]: false };
		}
	}

	function expiryText(iso: string): string {
		if (!iso) return 'never expires';
		const t = Date.parse(iso);
		if (!Number.isFinite(t)) return `expires ${iso}`;
		return `expires ${new Date(t).toLocaleString()}`;
	}

	async function refresh() {
		loading = true;
		consoles = await listConsoles();
		loading = false;
	}

	onMount(refresh);
</script>

<div class="mx-auto flex w-full max-w-4xl flex-col gap-5 p-4 sm:p-6">
	<header class="flex items-center gap-2">
		<TvIcon class="size-5" />
		<h1 class="h4">OBS overlays (Studio)</h1>
		<button
			class="ml-auto btn preset-tonal btn-sm"
			onclick={refresh}
			disabled={loading}
			aria-label="Refresh consoles"
		>
			{#if loading}<LoaderIcon class="size-4 animate-spin" />{:else}<RefreshCwIcon
					class="size-4"
				/>{/if}
			<span>Refresh</span>
		</button>
	</header>

	{#if !authorized}
		<div class="card preset-tonal p-6 text-sm">
			You don't have permission to view the OBS sources. Ask an admin for a role that carries the
			<code>overlay.mint</code> scope (the <code>overlay_manager</code> role does).
		</div>
	{:else}
		<ol class="flex list-decimal flex-col gap-1 card preset-tonal p-4 pl-8 text-sm">
			<li>
				Pick a <strong>console</strong> below — the overlay finds whichever host is showing it.
			</li>
			<li>
				<strong>Mint a spectator key</strong> for it. The key is scoped to that host box only and is
				shown once; it expires after 90 days and can be revoked from
				<a href={resolve('/admin/tokens/')} class="anchor">Tokens</a>.
			</li>
			<li>
				Copy the overlay's URL and add it in OBS as a <strong>Browser Source</strong> at the size shown.
			</li>
			<li>
				Every overlay renders <strong>transparent</strong> and targets by console name — it survives the
				box being recreated.
			</li>
		</ol>

		<label class="label max-w-md">
			<span class="label-text text-xs">Name overrides (optional, applied to all)</span>
			<input class="input font-mono text-xs" bind:value={names} placeholder="SCRAPED:Display,…" />
		</label>

		{#if loading}
			<div class="flex items-center gap-2 p-4 text-sm text-surface-600-400">
				<LoaderIcon class="size-4 animate-spin" /> Loading live consoles…
			</div>
		{:else if consoles.length === 0}
			<div class="card preset-tonal p-6 text-sm text-surface-600-400">
				No live consoles right now. Start a game on a scraped host (or check its System Link lobby)
				and hit Refresh.
			</div>
		{:else}
			<div class="flex flex-col gap-3">
				{#each consoles as c (c.console)}
					{@const key = keys[c.console]}
					<div class="flex flex-col gap-2 card preset-tonal p-4">
						<div class="flex items-center gap-2">
							<MonitorIcon class="size-4 opacity-70" />
							<span class="text-base font-semibold">{c.console}</span>
							{#if c.is_local}
								<span class="chip preset-filled-primary-500 text-xs">host</span>
							{:else}
								<span class="chip preset-tonal text-xs">lobby</span>
							{/if}
							<span class="ml-auto font-mono text-xs text-surface-500">on {c.instance}</span>
						</div>

						<div class="flex flex-wrap items-center gap-2">
							<button
								class="btn preset-filled-primary-500 btn-sm"
								onclick={() => mint(c)}
								disabled={minting[c.console]}
							>
								{#if minting[c.console]}<LoaderIcon class="size-4 animate-spin" />{:else}<KeyIcon
										class="size-4"
									/>{/if}
								<span>{key ? 'Mint a new spectator key' : 'Mint spectator key'}</span>
							</button>
							{#if key}
								<span class="font-mono text-xs text-surface-600-400">{key.kid}</span>
								<span class="text-xs text-surface-500">{expiryText(key.expires_at)}</span>
							{:else}
								<span class="text-xs text-surface-500">
									No key yet — the URLs below fall back to the legacy window path until one is
									minted.
								</span>
							{/if}
						</div>

						<div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
							{#each OVERLAYS as o (o.id)}
								<button
									class="btn flex-col items-start gap-0 preset-tonal py-1.5 btn-sm"
									title={spectatorURL(c.console, o.path)}
									disabled={!key}
									onclick={() => copy(spectatorURL(c.console, o.path))}
								>
									<span class="flex w-full items-center gap-1">
										<CopyIcon class="size-3.5" /><span class="font-medium">{o.label}</span>
									</span>
									<span class="text-[10px] text-surface-500">{o.size}</span>
								</button>
							{/each}
						</div>

						<div class="flex flex-col gap-2">
							<button
								class="btn self-start preset-tonal btn-sm"
								onclick={() =>
									(showLegacy = { ...showLegacy, [c.console]: !showLegacy[c.console] })}
								aria-expanded={!!showLegacy[c.console]}
							>
								<ShieldOffIcon class="size-3.5" />
								<span class="text-xs">
									{showLegacy[c.console] ? 'Hide' : 'Show'} legacy (window) URLs
								</span>
							</button>
							{#if showLegacy[c.console]}
								<p class="text-xs text-surface-500">
									Legacy (window): no key, the anonymous console door. Only works for a box's OWN
									console name, and only while the door is open server-side.
								</p>
								<div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
									{#each OVERLAYS as o (o.id)}
										<button
											class="btn flex-col items-start gap-0 preset-outlined py-1.5 btn-sm"
											title={legacyURL(c.console, o.path)}
											onclick={() => copy(legacyURL(c.console, o.path))}
										>
											<span class="flex w-full items-center gap-1">
												<CopyIcon class="size-3.5" /><span class="font-medium">{o.label}</span>
											</span>
											<span class="text-[10px] text-surface-500">legacy (window)</span>
										</button>
									{/each}
								</div>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		{/if}

		<p class="max-w-prose text-xs text-surface-600-400">
			Overlays read cartographer's live feed by console name, authenticated by the spectator key in
			the URL — treat the URL like a password. Fonts are self-hosted, so the graphics render
			correctly on an offline LAN. In OBS, leave <em>Shutdown source when not visible</em> off — the post-game
			report latches the final match time while the game is still live.
		</p>
	{/if}
</div>
