<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import {
		ActivityIcon,
		ArrowLeftIcon,
		BugIcon,
		EyeIcon,
		Gamepad2Icon,
		LoaderIcon,
		MapIcon,
		PlayIcon,
		RadarIcon,
		ServerIcon,
		SquareIcon,
		TagIcon,
		Trash2Icon,
		WifiIcon
	} from '@lucide/svelte';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { adminGet, adminPost, adminDelete, AdminFetchError } from '$lib/utils/admin-api';
	import { auth } from '$lib/stores/auth.svelte';
	import { confirmToast, toastPromise } from '$lib/stores/toaster';
	import { scraperWSV2 } from '$lib/stores/scraper-ws-v2.svelte';
	import type { EnvelopeTypeV2 } from '$lib/types/scraper-v2';
	import type { ContainerDetail, ContainerStatus } from '$lib/types/containers';
	import PageHeader from '$lib/components/ui/PageHeader.svelte';
	import UpstreamBanner from '$lib/components/ui/UpstreamBanner.svelte';
	import Card from '$lib/components/ui/Card.svelte';
	import StatTile from '$lib/components/debug/shared/StatTile.svelte';

	let { data } = $props();
	const name = $derived(data.name);

	type RowStatus = ContainerStatus | 'loading' | string;
	type BusyAction = 'start' | 'stop' | 'delete';

	let detail = $state<ContainerDetail | null>(null);
	let status = $state<RowStatus>('loading');
	let loading = $state(true);
	let busyAction = $state<BusyAction | null>(null);

	let pollTimer: ReturnType<typeof setInterval> | null = null;

	const isRunning = $derived(status === 'running');

	const summary = $derived(scraperWSV2.hostSummaries[name]);
	const xbox = $derived(scraperWSV2.xbox[name] ?? null);
	const game = $derived(scraperWSV2.game[name] ?? null);

	// Subscribe to the per-instance classes the landing page surfaces so the
	// stat tiles light up. Summary subscription happens at the /pod/ level for
	// the fleet view, but a direct nav to /pod/[name]/ needs to opt in too.
	const LANDING_CLASSES: EnvelopeTypeV2[] = ['xbox', 'game'];

	function statusBadgeClass(s: RowStatus): string {
		switch (s) {
			case 'running':
				return 'badge preset-filled-success-500';
			case 'exited':
			case 'stopped':
				return 'badge preset-tonal-error';
			case 'created':
			case 'paused':
			case 'stopping':
				return 'badge preset-tonal-warning';
			case 'loading':
				return 'badge preset-tonal';
			default:
				return 'badge preset-tonal-surface';
		}
	}

	const phaseBadgeClass: Record<string, string> = {
		idle: 'preset-tonal',
		ready: 'preset-filled-warning-500',
		live: 'preset-filled-success-500'
	};

	type StatusKind = 'on' | 'off' | 'warn' | 'info' | 'neutral' | 'pointer' | 'none';
	const phase = $derived(summary?.phase ?? null);
	const phaseTileKind = $derived<StatusKind>(
		!isRunning ? 'off' : phase === 'live' ? 'on' : phase === 'ready' ? 'warn' : 'neutral'
	);

	const titleDisplay = $derived(summary?.title || detail?.scraper?.title || '—');
	const xboxNameDisplay = $derived(xbox?.name || detail?.scraper?.xbox_name || '—');
	const mapDisplay = $derived(summary?.map || '—');
	const variantDisplay = $derived(
		game?.config?.variant_name || game?.config?.gametype || summary?.gametype || '—'
	);

	async function loadDetail() {
		try {
			const d = await adminGet<ContainerDetail>(`containers/${encodeURIComponent(name)}/detail`);
			detail = d;
			status = d.status;
		} catch (err) {
			if (err instanceof AdminFetchError && err.status === 404) {
				detail = null;
				status = 'unknown';
			} else {
				console.warn('detail fetch failed', err);
				status = 'unknown';
			}
		} finally {
			loading = false;
		}
	}

	function startPolling() {
		stopPolling();
		pollTimer = setInterval(() => {
			if (document.visibilityState !== 'visible') return;
			loadDetail();
		}, 3000);
	}

	function stopPolling() {
		if (pollTimer !== null) {
			clearInterval(pollTimer);
			pollTimer = null;
		}
	}

	async function handleStart() {
		busyAction = 'start';
		status = 'loading';
		try {
			await toastPromise(adminPost(`containers/${encodeURIComponent(name)}/start`), {
				loading: { title: 'Starting', description: name },
				// The scraper attaches asynchronously (wire mode answers 202): the
				// phase tile reads "attaching…" until the list phase changes.
				success: { title: 'Started', description: `${name} · scraper attaching…` },
				errorTitle: 'Start failed'
			});
		} catch {
			// toast already shown
		} finally {
			busyAction = null;
			await loadDetail();
		}
	}

	async function handleStop() {
		const ok = await confirmToast({
			type: 'warning',
			title: 'Stop pod?',
			description: `${name} — this will end the current session.`,
			confirmLabel: 'Stop',
			cancelLabel: 'Keep running'
		});
		if (!ok) return;
		busyAction = 'stop';
		status = 'loading';
		try {
			await toastPromise(adminPost(`containers/${encodeURIComponent(name)}/stop`), {
				loading: { title: 'Stopping', description: name },
				success: { title: 'Stopped', description: name },
				errorTitle: 'Stop failed'
			});
		} catch {
			// toast already shown
		} finally {
			busyAction = null;
			await loadDetail();
		}
	}

	async function handleDelete() {
		const runningWarning = isRunning ? ' It is currently running and will be force-stopped.' : '';
		const ok = await confirmToast({
			type: isRunning ? 'error' : 'warning',
			title: 'Delete container',
			description: `Permanently remove ${name}?${runningWarning}`,
			confirmLabel: 'Delete'
		});
		if (!ok) return;
		busyAction = 'delete';
		try {
			await toastPromise(adminDelete(`containers/${encodeURIComponent(name)}`), {
				loading: { title: 'Deleting', description: name },
				success: { title: 'Deleted', description: name },
				errorTitle: 'Delete failed'
			});
			await goto(resolve('/admin/pod/'));
		} catch {
			busyAction = null;
		}
	}

	onMount(async () => {
		if (auth.token) scraperWSV2.connect(auth.token);
		scraperWSV2.subscribeSummary();
		await loadDetail();
		startPolling();
	});

	$effect(() => {
		scraperWSV2.subscribeInstance(name, LANDING_CLASSES);
	});

	onDestroy(() => {
		stopPolling();
		scraperWSV2.unsubscribeInstance(name, LANDING_CLASSES);
		scraperWSV2.unsubscribeSummary();
		scraperWSV2.disconnect();
	});
</script>

<div class="mx-auto flex max-w-7xl flex-col gap-4">
	<div class="flex items-center justify-between gap-2">
		<a class="flex items-center gap-1 anchor text-sm" href={resolve('/admin/pod/')}>
			<ArrowLeftIcon class="size-4" />
			Back to pods
		</a>
	</div>

	<PageHeader title={name}>
		{#snippet actions()}
			<span class={statusBadgeClass(status)}>{status}</span>
			{#if phase}
				<span class="badge {phaseBadgeClass[phase] ?? 'preset-tonal'} text-[10px] uppercase">
					{phase}
				</span>
			{/if}
			{#if isRunning}
				<button
					type="button"
					class="btn preset-tonal-error btn-sm"
					disabled={loading || busyAction !== null}
					onclick={handleStop}
				>
					{#if busyAction === 'stop'}
						<LoaderIcon class="size-4 animate-spin" />
					{:else}
						<SquareIcon class="size-4" />
					{/if}
					<span>Stop</span>
				</button>
			{:else}
				<button
					type="button"
					class="preset-filled-primary btn btn-sm"
					disabled={loading || busyAction !== null}
					onclick={handleStart}
				>
					{#if busyAction === 'start'}
						<LoaderIcon class="size-4 animate-spin" />
					{:else}
						<PlayIcon class="size-4" />
					{/if}
					<span>Start</span>
				</button>
			{/if}
			<button
				type="button"
				class="btn preset-tonal-error btn-sm"
				disabled={busyAction !== null}
				onclick={handleDelete}
			>
				{#if busyAction === 'delete'}
					<LoaderIcon class="size-4 animate-spin" />
				{:else}
					<Trash2Icon class="size-4" />
				{/if}
				<span>Delete</span>
			</button>
		{/snippet}
	</PageHeader>

	<UpstreamBanner />

	{#snippet wsIcon()}<WifiIcon class="size-3.5" />{/snippet}
	{#snippet phaseIcon()}<ActivityIcon class="size-3.5" />{/snippet}
	{#snippet xboxIcon()}<ServerIcon class="size-3.5" />{/snippet}
	{#snippet titleIcon()}<TagIcon class="size-3.5" />{/snippet}
	{#snippet mapIcon()}<MapIcon class="size-3.5" />{/snippet}
	{#snippet variantIcon()}<Gamepad2Icon class="size-3.5" />{/snippet}

	<div class="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
		<StatTile
			label="websockets"
			display={scraperWSV2.connected ? 'connected' : 'disconnected'}
			statusKind={scraperWSV2.connected ? 'on' : 'off'}
			title={scraperWSV2.lastError ?? (scraperWSV2.connected ? 'connected' : 'disconnected')}
			icon={wsIcon}
		/>
		<StatTile
			label="scraper phase"
			display={phase ?? (isRunning ? 'attaching…' : '—')}
			statusKind={phaseTileKind}
			title={isRunning ? `scraper running · phase ${phase ?? 'attaching'}` : 'scraper stopped'}
			icon={phaseIcon}
		/>
		<StatTile
			label="xbox"
			display={xboxNameDisplay}
			statusKind={xboxNameDisplay === '—' ? 'none' : 'on'}
			title={xboxNameDisplay}
			icon={xboxIcon}
		/>
		<StatTile
			label="title"
			display={titleDisplay}
			statusKind={titleDisplay === '—' ? 'none' : 'on'}
			title={titleDisplay}
			icon={titleIcon}
		/>
		<StatTile
			label="map"
			display={mapDisplay}
			statusKind={mapDisplay === '—' ? 'none' : 'on'}
			title={mapDisplay}
			icon={mapIcon}
		/>
		<StatTile
			label="variant"
			display={variantDisplay}
			statusKind={variantDisplay === '—' ? 'none' : 'on'}
			title={variantDisplay}
			icon={variantIcon}
		/>
	</div>

	{#if summary?.score_summary}
		<Card size="sm">
			<div class="text-xs font-semibold tracking-wide text-surface-700-300 uppercase">Score</div>
			<div class="mt-1 font-mono text-sm">{summary.score_summary}</div>
		</Card>
	{/if}

	<div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
		<a
			href={resolve('/admin/pod/[name]/view', { name })}
			class="block rounded-container transition hover:bg-surface-200-800 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:outline-none"
		>
			<Card size="md" tone="filled" class="flex h-full flex-col items-start gap-2">
				<div class="flex items-center gap-2">
					<EyeIcon class="size-5 text-primary-500" />
					<span class="font-semibold">View</span>
				</div>
				<p class="text-sm text-surface-700-300">
					Live screen (noVNC) + Xbox controller for live interaction.
				</p>
			</Card>
		</a>
		<a
			href={resolve('/admin/pod/[name]/debug', { name })}
			class="block rounded-container transition hover:bg-surface-200-800 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:outline-none"
		>
			<Card size="md" tone="filled" class="flex h-full flex-col items-start gap-2">
				<div class="flex items-center gap-2">
					<BugIcon class="size-5 text-primary-500" />
					<span class="font-semibold">Debug</span>
				</div>
				<p class="text-sm text-surface-700-300">
					Per-envelope Pretty/JSON tabs for every scraped class on this instance.
				</p>
			</Card>
		</a>
		<a
			href={resolve('/admin/pod/[name]/probe', { name })}
			class="block rounded-container transition hover:bg-surface-200-800 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:outline-none"
		>
			<Card size="md" tone="filled" class="flex h-full flex-col items-start gap-2">
				<div class="flex items-center gap-2">
					<RadarIcon class="size-5 text-primary-500" />
					<span class="font-semibold">Probe</span>
				</div>
				<p class="text-sm text-surface-700-300">
					On-demand state-inputs + score-probe dump from the active GameReader plugin.
				</p>
			</Card>
		</a>
	</div>

	{#if loading}
		<Card size="sm">
			<div class="flex items-center gap-2 text-sm">
				<LoaderIcon class="size-4 animate-spin" />
				<span>Loading container details…</span>
			</div>
		</Card>
	{:else if !detail}
		<Card size="sm">
			<div class="text-sm">
				No container record for <code>{name}</code>. It may be an external QMP orphan or it may have
				been deleted.
				<a class="anchor" href={resolve('/admin/pod/')}>Back to pods</a>.
			</div>
		</Card>
	{:else}
		<Card size="sm">
			<div class="mb-2 text-xs font-semibold tracking-wide text-surface-700-300 uppercase">
				Container
			</div>
			<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
				<dt class="text-surface-500-400">status</dt>
				<dd>{detail.status}</dd>
				<dt class="text-surface-500-400">created</dt>
				<dd class="font-mono">{detail.info.created || '—'}</dd>
				<dt class="text-surface-500-400">index</dt>
				<dd class="font-mono">{detail.info.index}</dd>
				<dt class="text-surface-500-400">scraper attached</dt>
				<dd>{detail.scraper?.running ? 'yes' : 'no'}</dd>
				<dt class="text-surface-500-400">xemu http</dt>
				<dd class="font-mono">{detail.info.ports.xemu_http}</dd>
				<dt class="text-surface-500-400">xemu https</dt>
				<dd class="font-mono">{detail.info.ports.xemu_https}</dd>
				<dt class="text-surface-500-400">xemu ws</dt>
				<dd class="font-mono">{detail.info.ports.xemu_ws}</dd>
				<dt class="text-surface-500-400">browser web</dt>
				<dd class="font-mono">{detail.info.ports.browser_web}</dd>
				<dt class="text-surface-500-400">browser vnc</dt>
				<dd class="font-mono">{detail.info.ports.browser_vnc}</dd>
			</dl>
		</Card>
	{/if}
</div>
