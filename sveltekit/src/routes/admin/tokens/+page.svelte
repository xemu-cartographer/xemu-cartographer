<script lang="ts">
	// /admin/tokens/ — authz design §9: mint, list and revoke opaque keys
	// (machine / spectator / device). The secret is shown exactly once, in
	// the reveal panel after a mint; the list only ever carries the kid.
	//
	// /admin/+layout.ts + ./+page.ts enforce isAdmin; the server checks
	// token.list / token.mint:<kind> (overlay.mint for spectator) /
	// token.revoke:<kid> on every call.

	import { onMount } from 'svelte';
	import {
		BanIcon,
		CheckIcon,
		ClockIcon,
		CopyIcon,
		KeyIcon,
		LoaderIcon,
		PlusIcon,
		RefreshCwIcon,
		SearchIcon,
		Trash2Icon
	} from '@lucide/svelte';
	import { auth } from '$lib/stores/auth.svelte';
	import { describeAsyncError, toaster, toastPromise } from '$lib/stores/toaster';
	import PageHeader from '$lib/components/ui/PageHeader.svelte';
	import Card from '$lib/components/ui/Card.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import DataTable from '$lib/components/ui/DataTable.svelte';
	import type { DataColumnGroup, SortState } from '$lib/components/ui/data-table';
	import {
		TOKEN_KINDS,
		listTokens,
		mintToken,
		revokeToken,
		spectatorScopesFor,
		type MintResult,
		type TokenKind,
		type TokenRow
	} from '$lib/utils/tokens-api';
	import { parseScope } from '$lib/utils/scopes';

	type KindFilter = TokenKind | '';
	type RowState = 'active' | 'expired' | 'revoked';

	const KIND_HELP: Record<TokenKind, string> = {
		machine:
			'LAN clients (nxdk saves sync, the legacy LAN_SAVES_TOKEN). Wildcards allowed, e.g. lan.*',
		spectator:
			'OBS browser sources. No wildcards; every scope must name the same one instance (overlay.read_state:<inst>, room.join:host:<inst>:<class>).',
		device:
			'Station hardware bound to one box. No wildcards; every scope must name the same one container (box.view:<name>, box.drive:<name>).'
	};

	let rows = $state<TokenRow[]>([]);
	let loading = $state(true);
	let kindFilter = $state<KindFilter>('');
	let showRevoked = $state(false);
	let filter = $state('');
	let sort = $state<SortState>({ key: 'created', dir: 'desc' });

	// Mint dialog.
	let mintOpen = $state(false);
	let mintBusy = $state(false);
	let mintKind = $state<TokenKind>('machine');
	let mintLabel = $state('');
	let mintScopes = $state('');
	let mintInstance = $state(''); // spectator / device helper
	let mintUser = $state('');
	let mintContainer = $state('');
	let mintStation = $state('');
	let mintExpires = $state('');

	// One-time reveal after a successful mint.
	let revealed = $state<MintResult | null>(null);

	// Revoke dialog.
	let revokeOpen = $state(false);
	let revokeBusy = $state(false);
	let revokeTarget = $state<TokenRow | null>(null);
	let revokeReason = $state('');

	function stateOf(r: TokenRow): RowState {
		if (r.revoked) return 'revoked';
		if (r.expires_at) {
			const t = Date.parse(r.expires_at);
			if (Number.isFinite(t) && t <= Date.now()) return 'expired';
		}
		return 'active';
	}

	function statePriority(s: RowState): number {
		return s === 'active' ? 0 : s === 'expired' ? 1 : 2;
	}

	function whenText(iso: string): string {
		if (!iso) return '—';
		const t = Date.parse(iso);
		if (!Number.isFinite(t)) return iso;
		const diff = t - Date.now();
		const abs = Math.abs(diff);
		const unit =
			abs < 60_000
				? [Math.round(abs / 1000), 's']
				: abs < 3_600_000
					? [Math.round(abs / 60_000), 'm']
					: abs < 86_400_000
						? [Math.round(abs / 3_600_000), 'h']
						: [Math.round(abs / 86_400_000), 'd'];
		return diff < 0 ? `${unit[0]}${unit[1]} ago` : `in ${unit[0]}${unit[1]}`;
	}

	function boundText(r: TokenRow): string {
		const parts: string[] = [];
		if (r.user) parts.push(`user ${r.user}`);
		if (r.container) parts.push(`box ${r.container}`);
		if (r.station_id) parts.push(`station ${r.station_id}`);
		if (r.gamertags?.length) parts.push(`tags ${r.gamertags.join(', ')}`);
		return parts.join(' · ');
	}

	async function load() {
		try {
			loading = true;
			rows = await listTokens(kindFilter);
		} catch (err) {
			toaster.error({ title: 'Load failed', description: describeAsyncError(err) });
		} finally {
			loading = false;
		}
	}

	const visible = $derived.by<TokenRow[]>(() => {
		const q = filter.trim().toLowerCase();
		return rows.filter((r) => {
			if (!showRevoked && r.revoked) return false;
			if (!q) return true;
			return (
				r.kid.toLowerCase().includes(q) ||
				r.label.toLowerCase().includes(q) ||
				r.kind.toLowerCase().includes(q) ||
				r.container.toLowerCase().includes(q) ||
				r.station_id.toLowerCase().includes(q) ||
				r.scopes.some((s) => s.toLowerCase().includes(q))
			);
		});
	});

	// ── Mint ────────────────────────────────────────────────────────────────
	function openMint() {
		mintKind = kindFilter || 'machine';
		mintLabel = '';
		mintScopes = mintKind === 'machine' ? 'lan.*' : '';
		mintInstance = '';
		mintUser = '';
		mintContainer = '';
		mintStation = '';
		mintExpires = '';
		revealed = null;
		mintOpen = true;
	}

	function onKindChange(k: TokenKind) {
		mintKind = k;
		if (k === 'machine' && !mintScopes.trim()) mintScopes = 'lan.*';
	}

	function fillInstanceScopes() {
		const inst = mintInstance.trim();
		if (!inst) return;
		if (mintKind === 'spectator') {
			mintScopes = spectatorScopesFor(inst).join('\n');
		} else if (mintKind === 'device') {
			const c = inst.toLowerCase();
			mintScopes = [`box.view:${c}`, `box.drive:${c}`].join('\n');
			if (!mintContainer.trim()) mintContainer = inst;
		}
	}

	const scopeLines = $derived(
		mintScopes
			.split(/[\n,]/)
			.map((s) => s.trim())
			.filter(Boolean)
	);
	// Client-side syntax pre-check with the same grammar the server applies
	// (ParseScope); the kind-specific rules (no wildcards on spectator/device,
	// one instance) are left to the server's 400.
	const badScopes = $derived(scopeLines.filter((s) => parseScope(s.toLowerCase()) === null));
	const mintValid = $derived(
		mintLabel.trim() !== '' && scopeLines.length > 0 && badScopes.length === 0
	);

	async function mint() {
		if (!mintValid || mintBusy) return;
		try {
			mintBusy = true;
			// datetime-local yields "YYYY-MM-DDTHH:mm" in local time; the
			// server wants RFC3339, so convert through Date.
			const expiresISO = mintExpires ? new Date(mintExpires).toISOString() : undefined;
			const result = await toastPromise(
				mintToken({
					kind: mintKind,
					label: mintLabel.trim(),
					scopes: scopeLines.map((s) => s.toLowerCase()),
					user: mintUser.trim() || undefined,
					container: mintContainer.trim() || undefined,
					station_id: mintStation.trim() || undefined,
					expires_at: expiresISO
				}),
				{
					loading: { title: 'Minting', description: mintLabel.trim() },
					success: (r) => ({ title: 'Key minted', description: r.kid }),
					errorTitle: 'Mint failed'
				}
			);
			revealed = result;
			mintOpen = false;
			await load();
		} catch {
			// toast already shown
		} finally {
			mintBusy = false;
		}
	}

	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toaster.success({ title: 'Copied', description: 'Key on the clipboard.' });
		} catch {
			toaster.error({ title: 'Copy failed', description: 'Select the key and copy it manually.' });
		}
	}

	// ── Revoke ──────────────────────────────────────────────────────────────
	function openRevoke(r: TokenRow) {
		revokeTarget = r;
		revokeReason = '';
		revokeOpen = true;
	}

	async function revoke() {
		if (!revokeTarget || revokeBusy) return;
		const target = revokeTarget;
		try {
			revokeBusy = true;
			await toastPromise(revokeToken(target.kid, revokeReason), {
				loading: { title: 'Revoking', description: target.kid },
				success: {
					title: 'Revoked',
					description: `${target.kid} — live sockets are being closed.`
				},
				errorTitle: 'Revoke failed'
			});
			revokeOpen = false;
			if (revealed?.kid === target.kid) revealed = null;
			await load();
		} catch {
			// toast already shown
		} finally {
			revokeBusy = false;
		}
	}

	onMount(() => {
		if (!auth.token) {
			toaster.error({ title: 'Not authenticated', description: 'Log in to manage tokens.' });
			return;
		}
		void load();
	});
</script>

<div class="mx-auto flex max-w-6xl flex-col gap-4 sm:gap-6">
	<PageHeader
		title="API tokens"
		description="Opaque keys for things that aren't a signed-in browser: machine keys for LAN clients, spectator keys for OBS browser sources, device keys for stations (tablets, headless LAN pulls). Each key carries its own scopes; the secret is shown once at mint and never again."
	/>

	{#if revealed}
		<Card class="flex flex-col gap-3 border border-warning-500/40">
			<div class="flex items-center gap-2">
				<KeyIcon class="size-4" />
				<span class="font-semibold">New {revealed.kind} key — copy it now</span>
				<span class="badge preset-tonal font-mono text-xs">{revealed.kid}</span>
				<button
					class="ml-auto btn-icon preset-tonal btn-sm"
					title="Dismiss"
					aria-label="Dismiss"
					onclick={() => (revealed = null)}
				>
					<CheckIcon class="size-4" />
				</button>
			</div>
			<p class="text-sm text-surface-600-400">
				This is the only time the secret is shown. Store it where the client reads it (the LAN
				client's
				<code>Authorization: Bearer</code> header, the OBS source URL's <code>?spectator=</code>,
				the screen's <code>?token=</code>). Losing it means minting a new key.
			</p>
			<div class="flex items-center gap-2">
				<code class="input flex-1 overflow-x-auto font-mono text-xs break-all select-all"
					>{revealed.token}</code
				>
				<button class="btn preset-filled btn-sm" onclick={() => copy(revealed?.token ?? '')}>
					<CopyIcon class="size-4" /><span>Copy</span>
				</button>
			</div>
			<div class="flex flex-wrap gap-1 text-xs">
				<span class="text-surface-500">Scopes:</span>
				{#each revealed.scopes as s (s)}
					<span class="badge preset-tonal font-mono">{s}</span>
				{/each}
				<span class="ml-auto text-surface-500">
					{revealed.expires_at ? `expires ${whenText(revealed.expires_at)}` : 'never expires'}
				</span>
			</div>
		</Card>
	{/if}

	<div class="flex flex-wrap items-center gap-2">
		<div class="field-group min-w-60 flex-1 grid-cols-[auto_1fr]">
			<div class="flex items-center justify-center preset-tonal px-3">
				<SearchIcon class="size-4" />
			</div>
			<input
				type="search"
				class="input"
				placeholder="Filter by kid, label, scope, box or station"
				bind:value={filter}
			/>
		</div>
		<select
			class="select w-auto"
			bind:value={kindFilter}
			onchange={() => void load()}
			aria-label="Kind"
		>
			<option value="">All kinds</option>
			{#each TOKEN_KINDS as k (k)}
				<option value={k}>{k}</option>
			{/each}
		</select>
		<label class="flex items-center gap-2 text-sm">
			<input type="checkbox" class="checkbox" bind:checked={showRevoked} />
			<span>Show revoked</span>
		</label>
		<button class="btn preset-tonal" onclick={() => load()} disabled={loading} aria-label="Refresh">
			{#if loading}<LoaderIcon class="size-4 animate-spin" />{:else}<RefreshCwIcon
					class="size-4"
				/>{/if}
			<span>Refresh</span>
		</button>
		<button class="btn preset-filled" onclick={openMint}>
			<PlusIcon class="size-4" />
			<span>Mint key</span>
		</button>
	</div>

	{#snippet kidCell({ row }: { row: TokenRow })}
		<div class="flex flex-col">
			<span class="font-medium">{row.label || '—'}</span>
			<span class="font-mono text-xs opacity-60">{row.kid}</span>
		</div>
	{/snippet}
	{#snippet kindCell({ row }: { row: TokenRow })}
		<span class="badge preset-tonal-primary font-mono text-xs">{row.kind}</span>
	{/snippet}
	{#snippet scopesCell({ row }: { row: TokenRow })}
		<div class="flex max-w-md flex-wrap gap-1" title={row.scopes.join('\n')}>
			{#each row.scopes.slice(0, 4) as s (s)}
				<span class="badge preset-tonal font-mono text-[10px]">{s}</span>
			{/each}
			{#if row.scopes.length > 4}
				<span class="badge preset-tonal text-[10px]">+{row.scopes.length - 4}</span>
			{/if}
			{#if row.scopes.length === 0}
				<span class="text-xs opacity-50">—</span>
			{/if}
		</div>
	{/snippet}
	{#snippet boundCell({ row }: { row: TokenRow })}
		<span class="text-xs">{boundText(row) || '—'}</span>
	{/snippet}
	{#snippet expiresCell({ row }: { row: TokenRow })}
		<span class="text-xs" title={row.expires_at || 'never'}>
			{row.expires_at ? whenText(row.expires_at) : 'never'}
		</span>
	{/snippet}
	{#snippet lastUsedCell({ row }: { row: TokenRow })}
		<span class="text-xs" title={row.last_used_at || 'never used'}
			>{whenText(row.last_used_at)}</span
		>
	{/snippet}
	{#snippet stateCell({ row }: { row: TokenRow })}
		{@const s = stateOf(row)}
		{#if s === 'revoked'}
			<span class="badge preset-tonal-error" title={row.revoked_at}>
				<BanIcon class="size-3" />
				<span>revoked</span>
			</span>
		{:else if s === 'expired'}
			<span class="badge preset-tonal-warning" title={row.expires_at}>
				<ClockIcon class="size-3" />
				<span>expired</span>
			</span>
		{:else}
			<span class="badge preset-tonal-success">
				<CheckIcon class="size-3" />
				<span>active</span>
			</span>
		{/if}
	{/snippet}
	{#snippet actionsCell({ row }: { row: TokenRow })}
		<div
			role="presentation"
			class="inline-flex items-center justify-end gap-1"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => e.stopPropagation()}
		>
			<button
				class="btn-icon preset-tonal-error btn-sm"
				title={row.kid === 'legacy-env' ? 'Unset LAN_SAVES_TOKEN instead' : 'Revoke'}
				onclick={() => openRevoke(row)}
				disabled={row.revoked || row.kid === 'legacy-env'}
			>
				<Trash2Icon class="size-4" />
			</button>
		</div>
	{/snippet}

	<Card size="flush" class="overflow-x-auto">
		<DataTable
			rows={visible}
			groups={[
				{
					columns: [
						{ key: 'label', label: 'Key', cell: kidCell },
						{ key: 'kind', label: 'Kind', cell: kindCell },
						{ key: 'scopes', label: 'Scopes', cell: scopesCell, sortable: false },
						{ key: 'bound', label: 'Bound to', cell: boundCell, sortable: false },
						{
							key: 'expires_at',
							label: 'Expires',
							cell: expiresCell,
							sortAccessor: (r) =>
								r.expires_at ? Date.parse(r.expires_at) : Number.MAX_SAFE_INTEGER
						},
						{
							key: 'last_used_at',
							label: 'Last used',
							cell: lastUsedCell,
							sortAccessor: (r) => (r.last_used_at ? Date.parse(r.last_used_at) : 0)
						},
						{
							key: 'created',
							label: 'Created',
							format: (r) => whenText(r.created),
							sortAccessor: (r) => Date.parse(r.created) || 0
						},
						{
							key: 'state',
							label: 'State',
							cell: stateCell,
							comparator: (a, b) => statePriority(stateOf(a)) - statePriority(stateOf(b))
						},
						{ key: 'actions', label: '', cell: actionsCell, sortable: false, align: 'right' }
					]
				}
			] satisfies DataColumnGroup<TokenRow>[]}
			rowKey={(r) => r.kid}
			density="comfortable"
			{sort}
			onSortChange={(s) => (sort = s)}
			secondarySort={{ key: 'label', dir: 'asc' }}
			loading={loading && visible.length === 0}
			emptyMessage={filter || kindFilter
				? 'No keys match.'
				: 'No keys yet. Mint one for a LAN client, an OBS source or a station.'}
		/>
	</Card>
</div>

<Dialog
	open={mintOpen}
	onClose={() => {
		if (!mintBusy) mintOpen = false;
	}}
	title="Mint key"
	size="lg"
>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			void mint();
		}}
		class="flex flex-col gap-3"
	>
		<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
			<label class="label">
				<span class="label-text">Kind</span>
				<select
					class="select"
					value={mintKind}
					onchange={(e) => onKindChange(e.currentTarget.value as TokenKind)}
					disabled={mintBusy}
				>
					{#each TOKEN_KINDS as k (k)}
						<option value={k}>{k}</option>
					{/each}
				</select>
			</label>
			<label class="label">
				<span class="label-text">Label</span>
				<input
					type="text"
					class="input"
					bind:value={mintLabel}
					placeholder={mintKind === 'machine' ? 'e.g. lan-sync box A' : 'e.g. obs:box1'}
					disabled={mintBusy}
					required
				/>
			</label>
		</div>
		<p class="text-xs opacity-60">{KIND_HELP[mintKind]}</p>

		{#if mintKind !== 'machine'}
			<div class="flex items-end gap-2">
				<label class="label flex-1">
					<span class="label-text">
						{mintKind === 'spectator' ? 'Instance (host box)' : 'Container'}
					</span>
					<input
						type="text"
						class="input"
						bind:value={mintInstance}
						placeholder="e.g. box1"
						disabled={mintBusy}
					/>
				</label>
				<button
					type="button"
					class="btn preset-tonal"
					onclick={fillInstanceScopes}
					disabled={mintBusy || !mintInstance.trim()}
				>
					Fill scopes
				</button>
			</div>
		{/if}

		<label class="label">
			<span class="label-text">Scopes (one per line)</span>
			<textarea
				class="textarea font-mono text-xs"
				rows="5"
				bind:value={mintScopes}
				placeholder={mintKind === 'machine'
					? 'lan.*'
					: mintKind === 'spectator'
						? 'overlay.read_state:box1\nroom.join:host:box1:game_filtered'
						: 'box.view:box1\nbox.drive:box1'}
				disabled={mintBusy}
			></textarea>
		</label>
		{#if badScopes.length}
			<p class="text-xs text-error-600-400">
				Not a valid scope: {badScopes.map((s) => `"${s}"`).join(', ')}
			</p>
		{/if}

		<details class="text-sm">
			<summary class="cursor-pointer text-xs opacity-70">Bindings and expiry (optional)</summary>
			<div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
				<label class="label">
					<span class="label-text">User id</span>
					<input type="text" class="input" bind:value={mintUser} disabled={mintBusy} />
				</label>
				<label class="label">
					<span class="label-text">Container</span>
					<input type="text" class="input" bind:value={mintContainer} disabled={mintBusy} />
				</label>
				<label class="label">
					<span class="label-text">Station id</span>
					<input type="text" class="input" bind:value={mintStation} disabled={mintBusy} />
				</label>
				<label class="label">
					<span class="label-text">Expires at (local time)</span>
					<input type="datetime-local" class="input" bind:value={mintExpires} disabled={mintBusy} />
					<span class="text-xs opacity-60">
						Blank = {mintKind === 'spectator' ? '90 days (server default)' : 'never'}.
					</span>
				</label>
			</div>
		</details>

		<div class="flex justify-end gap-2">
			<button
				type="button"
				class="btn preset-tonal"
				onclick={() => (mintOpen = false)}
				disabled={mintBusy}
			>
				Cancel
			</button>
			<button type="submit" class="btn preset-filled" disabled={mintBusy || !mintValid}>
				{#if mintBusy}<LoaderIcon class="size-4 animate-spin" />{:else}<KeyIcon
						class="size-4"
					/>{/if}
				<span>Mint</span>
			</button>
		</div>
	</form>
</Dialog>

<Dialog
	open={revokeOpen}
	onClose={() => {
		if (!revokeBusy) revokeOpen = false;
	}}
	title="Revoke key"
>
	{#if revokeTarget}
		<form
			onsubmit={(e) => {
				e.preventDefault();
				void revoke();
			}}
			class="flex flex-col gap-3"
		>
			<p class="text-sm">
				Revoke <span class="font-mono">{revokeTarget.kid}</span>
				{#if revokeTarget.label}(<span class="font-medium">{revokeTarget.label}</span>){/if}? Every
				request with it fails from now on and any live WebSocket carrying it is closed. This cannot
				be undone — mint a new key instead.
			</p>
			<label class="label">
				<span class="label-text">Reason (optional, written to audit log)</span>
				<input
					type="text"
					class="input"
					bind:value={revokeReason}
					placeholder="e.g. rotated after event"
					disabled={revokeBusy}
				/>
			</label>
			<div class="flex justify-end gap-2">
				<button
					type="button"
					class="btn preset-tonal"
					onclick={() => (revokeOpen = false)}
					disabled={revokeBusy}
				>
					Cancel
				</button>
				<button type="submit" class="preset-filled-error btn" disabled={revokeBusy}>
					{#if revokeBusy}<LoaderIcon class="size-4 animate-spin" />{/if}
					<span>Revoke</span>
				</button>
			</div>
		</form>
	{/if}
</Dialog>
