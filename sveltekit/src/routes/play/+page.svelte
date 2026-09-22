<script lang="ts">
	// Player-hosting PLAY TAB. A phase-aware, WS-driven surface onto the
	// /api/play/* endpoints — the player never loads noVNC here. The host-runner
	// drives the box's menus; this UI just picks the game, readies up, and shows
	// live state off the scraper `game` feed.
	//
	//   catalog  → pick a game + request a box       (GET /isos, POST /request)
	//   provisioning → box booting, waiting to resolve (GET /current)
	//   lobby    → map/gametype, ready, joiners       (GET /options, POST selection/ready)
	//   live     → live scoreboard                    (WS host:<inst>:game/tick/scenario)
	//   postgame → final scores → back to lobby       (WS host:<inst>:previous_game)
	//
	// Sibling to — never reusing — the admin screen/VNC path (routes/containers).
	import { onMount, onDestroy } from 'svelte';
	import { resolve } from '$app/paths';
	import { Gamepad2Icon, LoaderIcon, PowerOffIcon } from '@lucide/svelte';
	import { apiBaseURL } from '$lib/utils/api-base';
	import { auth } from '$lib/stores/auth.svelte';
	import { scraperWSV2 } from '$lib/stores/scraper-ws-v2.svelte';
	import type { EnvelopeTypeV2 } from '$lib/types/scraper-v2';
	import {
		activeInstance,
		derivePlayHostPhase,
		resolvedFromCurrent,
		type CurrentResponse,
		type IsoOption,
		type OptionsResponse,
		type RequestResponse
	} from '$lib/utils/play-hosting';
	import PlayCatalog from '$lib/components/play/host/PlayCatalog.svelte';
	import { listIsoMaps, type IsoMap } from '$lib/utils/maps';
	import PlayLobby from '$lib/components/play/host/PlayLobby.svelte';
	import PlayScoreboard from '$lib/components/play/host/PlayScoreboard.svelte';

	// The `game` feed carries phase + scores; `tick` carries alive/health;
	// `scenario` the map; `previous_game` the just-ended match for the postgame.
	const HOST_CLASSES: EnvelopeTypeV2[] = ['game', 'tick', 'scenario', 'previous_game'];
	const PENDING_KEY = 'play:pending';
	const PENDING_TTL_MS = 10 * 60 * 1000; // drop a stale boot hint after 10 min

	// --- Catalog state ---
	let isos = $state<IsoOption[]>([]);
	let isosLoading = $state(true);
	let isosError = $state<string | null>(null);
	let requestingId = $state<string | null>(null);
	let requestError = $state<string | null>(null);
	let mapsByIso = $state<Record<string, IsoMap[]>>({});

	// --- Session state ---
	let current = $state<CurrentResponse | null>(null);
	let options = $state<OptionsResponse | null>(null);
	let pendingInstance = $state<string | null>(null);
	let postgameDismissed = $state(false);
	let actionBusy = $state(false);
	let actionError = $state<string | null>(null);
	let hostDisabled = $state(false);

	let pollTimer: ReturnType<typeof setInterval> | null = null;
	let optionsFor: string | null = null;
	let lastEndedAt: string | null = null;

	// --- Derived phase model ---
	const resolvedInstance = $derived(resolvedFromCurrent(current));
	const active = $derived(activeInstance({ resolvedInstance, pendingInstance }));
	const gamePayload = $derived(active ? (scraperWSV2.game[active] ?? null) : null);
	const tickPayload = $derived(active ? (scraperWSV2.tick[active] ?? null) : null);
	const scenarioPayload = $derived(active ? (scraperWSV2.scenario[active] ?? null) : null);
	const previousGame = $derived(active ? (scraperWSV2.previousGame[active] ?? null) : null);
	const phase = $derived(
		derivePlayHostPhase({
			resolvedInstance,
			pendingInstance,
			gamePhase: gamePayload?.phase ?? null,
			hasPreviousGame: !!previousGame,
			postgameDismissed
		})
	);

	// --- WS subscription follows the active instance ---
	let subscribedTo: string | null = null;
	$effect(() => {
		const a = active;
		if (a === subscribedTo) return;
		if (subscribedTo) scraperWSV2.unsubscribeInstance(subscribedTo, HOST_CLASSES);
		if (a) scraperWSV2.subscribeInstance(a, HOST_CLASSES);
		subscribedTo = a;
	});

	// A fresh match result re-shows the postgame card; going live clears it.
	$effect(() => {
		if (previousGame && previousGame.ended_at !== lastEndedAt) {
			lastEndedAt = previousGame.ended_at;
			postgameDismissed = false;
		}
	});
	$effect(() => {
		if (gamePayload?.phase === 'live') postgameDismissed = false;
	});

	// Load the live map/gametype list when a box resolves; keep retrying until the
	// disc is enumerable (folded into the poll below).
	$effect(() => {
		const inst = resolvedInstance;
		if (inst && inst !== optionsFor) {
			optionsFor = inst;
			void loadOptions();
		} else if (!inst) {
			optionsFor = null;
			options = null;
		}
	});

	// --- Pending (boot hint) persistence ---
	function setPending(name: string) {
		pendingInstance = name;
		postgameDismissed = false;
		try {
			localStorage.setItem(PENDING_KEY, JSON.stringify({ instance: name, at: Date.now() }));
		} catch {
			// localStorage unavailable — in-memory pending still works this session.
		}
	}
	function clearPending() {
		pendingInstance = null;
		try {
			localStorage.removeItem(PENDING_KEY);
		} catch {
			// ignore
		}
	}
	function restorePending() {
		try {
			const raw = localStorage.getItem(PENDING_KEY);
			if (!raw) return;
			const v = JSON.parse(raw) as { instance?: string; at?: number };
			if (v?.instance && typeof v.at === 'number' && Date.now() - v.at < PENDING_TTL_MS) {
				pendingInstance = v.instance;
			} else {
				localStorage.removeItem(PENDING_KEY);
			}
		} catch {
			// ignore
		}
	}
	function derivedBoxName(): string | null {
		const id = auth.user?.id;
		return id ? `play-${id.toLowerCase()}` : null;
	}
	function extractInstanceName(msg: unknown): string | null {
		if (typeof msg !== 'string') return null;
		const m = msg.match(/\(([^)]+)\)/);
		return m ? m[1] : null;
	}

	function authHeaders(json = false): Record<string, string> {
		const h: Record<string, string> = {};
		if (auth.token) h.Authorization = auth.token;
		if (json) h['Content-Type'] = 'application/json';
		return h;
	}

	// --- API calls ---
	async function loadIsos() {
		isosLoading = true;
		isosError = null;
		try {
			const res = await fetch(`${apiBaseURL()}/api/play/isos`, { headers: authHeaders() });
			if (!res.ok) {
				isosError = `Couldn't load the game library (${res.status}).`;
				return;
			}
			isos = (await res.json()) as IsoOption[];
			void loadCatalogMaps();
		} catch {
			isosError = 'Network error loading the game library.';
		} finally {
			isosLoading = false;
		}
	}

	// Load each catalog game's maps (with thumbnails) in parallel so the cards
	// can surface what a build actually ships. Best-effort — a game with no maps
	// simply shows none.
	async function loadCatalogMaps() {
		const entries = await Promise.all(
			isos.map(async (iso) => [iso.id, await listIsoMaps(iso.id, 'play')] as const)
		);
		mapsByIso = Object.fromEntries(entries);
	}

	async function refreshCurrent() {
		if (!auth.token) {
			current = null;
			return;
		}
		try {
			const res = await fetch(`${apiBaseURL()}/api/play/current`, { headers: authHeaders() });
			if (res.status === 503) {
				hostDisabled = true;
				return;
			}
			hostDisabled = false;
			if (!res.ok) return; // transient — keep last-known state, avoid flicker
			current = (await res.json()) as CurrentResponse;
			// Server resolved us into a box → the local boot hint is redundant.
			if (resolvedFromCurrent(current)) clearPending();
		} catch {
			// network hiccup — keep last-known state; next poll retries
		}
	}

	async function loadOptions() {
		try {
			const res = await fetch(`${apiBaseURL()}/api/play/options`, { headers: authHeaders() });
			if (!res.ok) return;
			options = (await res.json()) as OptionsResponse;
		} catch {
			// ignore — retried while unavailable by the poll loop
		}
	}

	async function requestInstance(isoId: string) {
		requestingId = isoId;
		requestError = null;
		try {
			const res = await fetch(`${apiBaseURL()}/api/play/request`, {
				method: 'POST',
				headers: authHeaders(true),
				body: JSON.stringify({ iso: isoId })
			});
			if (res.status === 201) {
				const data = (await res.json()) as RequestResponse;
				setPending(data.instance);
			} else if (res.status === 409) {
				// Already have a box — recover its name so we jump into it.
				const err = (await res.json().catch(() => ({}))) as { error?: string };
				const name = extractInstanceName(err.error) ?? derivedBoxName();
				if (name) setPending(name);
				else requestError = err.error ?? 'You already have a box.';
			} else {
				const err = (await res.json().catch(() => ({}))) as { error?: string };
				requestError = err.error ?? `Couldn't start a box (${res.status}).`;
			}
			await refreshCurrent();
		} catch {
			requestError = 'Network error starting a box.';
		} finally {
			requestingId = null;
		}
	}

	async function postAction(path: string, body?: unknown) {
		if (!active || actionBusy) return;
		actionBusy = true;
		actionError = null;
		try {
			const res = await fetch(`${apiBaseURL()}/api/play/${path}`, {
				method: 'POST',
				headers: authHeaders(true),
				body: body !== undefined ? JSON.stringify(body) : undefined
			});
			if (res.ok) {
				const status = await res.json();
				current = { instance: active, status, reap: current?.reap ?? null };
			} else {
				const err = (await res.json().catch(() => ({}))) as { error?: string };
				actionError = err.error ?? `Action failed (${res.status}).`;
			}
		} catch {
			actionError = 'Network error.';
		} finally {
			actionBusy = false;
			void refreshCurrent();
		}
	}

	const selectGame = (map: string, gametype: string) => postAction('selection', { map, gametype });
	const teardown = () => postAction('teardown');

	function onFocus() {
		void refreshCurrent();
	}

	onMount(() => {
		restorePending();
		if (auth.token) scraperWSV2.connect(auth.token);
		void loadIsos();
		void refreshCurrent();
		window.addEventListener('focus', onFocus);
		pollTimer = setInterval(() => {
			if (document.visibilityState !== 'visible') return;
			void refreshCurrent();
			// Keep re-polling the disc's map/gametype list until it's not just
			// enumerable but COMPLETE — i.e. available AND the async custom-variant
			// read has finished (gametypes_pending false). This is what makes the
			// picker pick up the real (built-ins + custom variants) list live,
			// WITHOUT a hard refresh, instead of freezing on the first partial read.
			if (resolvedInstance && (!options?.available || options?.gametypes_pending)) {
				void loadOptions();
			}
		}, 4000);
	});

	onDestroy(() => {
		window.removeEventListener('focus', onFocus);
		if (pollTimer !== null) clearInterval(pollTimer);
		if (subscribedTo) scraperWSV2.unsubscribeInstance(subscribedTo, HOST_CLASSES);
		scraperWSV2.disconnect();
	});
</script>

<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
	<header class="flex items-center gap-2">
		<Gamepad2Icon class="size-5" />
		<h1 class="h4">Play</h1>
		{#if active && phase !== 'catalog'}
			<span class="ml-auto flex items-center gap-1 text-xs">
				<span
					class="size-2 rounded-full {scraperWSV2.connected ? 'bg-success-500' : 'bg-surface-500'}"
				></span>
				<span class="text-surface-500">{scraperWSV2.connected ? 'Live' : 'Connecting…'}</span>
			</span>
		{/if}
	</header>

	{#if hostDisabled}
		<div class="flex items-center gap-2 card preset-tonal-warning p-4 text-sm">
			<PowerOffIcon class="size-4 shrink-0" />
			<span>Player hosting isn't enabled on this server right now.</span>
		</div>
	{/if}

	{#if requestError}
		<div class="card preset-tonal-error p-3 text-sm">{requestError}</div>
	{/if}
	{#if actionError}
		<div class="card preset-tonal-error p-3 text-sm">{actionError}</div>
	{/if}

	{#if phase === 'catalog'}
		<PlayCatalog
			{isos}
			{mapsByIso}
			loading={isosLoading}
			error={isosError}
			{requestingId}
			onrequest={requestInstance}
		/>
		<p class="text-xs text-surface-500">
			Playing under a different name? Add the gamertag you host with in
			<a class="anchor" href={resolve('/settings/')}>Settings</a>.
		</p>
	{:else if phase === 'provisioning'}
		<div class="flex flex-col items-center gap-3 card preset-tonal p-10 text-center">
			<LoaderIcon class="size-8 animate-spin" />
			<p class="font-medium">Starting your box…</p>
			<p class="max-w-md text-sm text-surface-600-400">
				Booting the game and joining you in. This usually takes under a minute.
			</p>
			<button
				type="button"
				class="btn preset-tonal btn-sm"
				onclick={() => {
					clearPending();
					void refreshCurrent();
				}}
			>
				Cancel
			</button>
		</div>
	{:else if phase === 'live' && active}
		<PlayScoreboard game={gamePayload} tick={tickPayload} scenario={scenarioPayload} />
	{:else if phase === 'postgame' && active}
		<PlayScoreboard
			game={previousGame?.game ?? gamePayload}
			tick={null}
			scenario={scenarioPayload}
			final
			onback={() => (postgameDismissed = true)}
		/>
	{:else if active}
		<!-- lobby -->
		{#key active}
			<PlayLobby
				instance={active}
				status={current?.status ?? null}
				{options}
				game={gamePayload}
				reap={current?.reap ?? null}
				busy={actionBusy}
				onselect={selectGame}
				onteardown={teardown}
			/>
		{/key}
	{/if}
</div>
