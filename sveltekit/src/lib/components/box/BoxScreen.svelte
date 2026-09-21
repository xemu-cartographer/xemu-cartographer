<script lang="ts" module>
	export type SnapshotSlot = { label: string; key: string; body: string };
</script>

<script lang="ts">
	import { CameraIcon, ExternalLinkIcon, RotateCcwIcon } from '@lucide/svelte';
	import { Popover, Portal } from '@skeletonlabs/skeleton-svelte';

	const DEFAULT_SNAPSHOTS: SnapshotSlot[] = [
		{ label: 'S1', key: 'F5', body: 'Trigger snapshot slot 1 (F5)?' },
		{ label: 'S2', key: 'F6', body: 'Trigger snapshot slot 2 (F6)?' },
		{ label: 'S3', key: 'F7', body: 'Trigger snapshot slot 3 (F7)?' },
		{ label: 'S4', key: 'F8', body: 'Trigger snapshot slot 4 (F8)?' }
	];

	let {
		name,
		src,
		running,
		loading = false,
		vncConnected = false,
		externalHref,
		snapshots = DEFAULT_SNAPSHOTS,
		onSnapshot,
		onReset,
		class: extraClass = ''
	}: {
		name: string;
		src: string;
		running: boolean;
		loading?: boolean;
		vncConnected?: boolean;
		externalHref?: string;
		snapshots?: SnapshotSlot[];
		onSnapshot?: (slot: SnapshotSlot) => void;
		onReset?: () => void;
		class?: string;
	} = $props();
</script>

<div class="flex flex-col overflow-hidden card p-0 {extraClass}">
	<div class="flex flex-none items-center justify-around gap-2 p-2 text-xs">
		<span class="text-surface-600-400">
			Screen
			<span class="ms-1 text-xs {vncConnected ? 'text-success-500' : 'text-surface-600-400'}">
				{vncConnected ? '●' : running ? '…' : '○'}
			</span>
		</span>
		{#if externalHref}
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			<a
				href={externalHref}
				target="_blank"
				rel="noopener"
				class="btn-icon preset-tonal btn-icon-sm"
				aria-label="Open in new tab"
				title="Open in new tab"
			>
				<ExternalLinkIcon class="size-4" />
			</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/if}
		<span class="flex"></span>
		<div class="ms-auto flex items-center gap-1">
			{#if onSnapshot}
				<Popover>
					<Popover.Trigger
						class="btn preset-tonal btn-sm"
						disabled={!vncConnected}
						aria-label="Snapshots"
					>
						<CameraIcon class="size-4" />
						<span>Snapshots</span>
					</Popover.Trigger>
					<Portal>
						<Popover.Positioner>
							<Popover.Content class="flex gap-1 card bg-surface-100-900 p-2">
								{#each snapshots as slot (slot.key)}
									<Popover.CloseTrigger
										class="btn preset-tonal btn-sm"
										onclick={() => onSnapshot?.(slot)}
									>
										{slot.label}
									</Popover.CloseTrigger>
								{/each}
							</Popover.Content>
						</Popover.Positioner>
					</Portal>
				</Popover>
			{/if}
			{#if onReset}
				<button
					type="button"
					class="btn-icon preset-tonal-warning btn-icon-sm"
					aria-label="Reset"
					title="Reset"
					disabled={!vncConnected}
					onclick={onReset}
				>
					<RotateCcwIcon class="size-4" />
				</button>
			{/if}
		</div>
	</div>
	{#if running}
		{#key src}
			<iframe {src} title="Screen of {name}" class="aspect-4/3 w-full border-0" allowfullscreen
			></iframe>
		{/key}
	{:else}
		<div class="flex flex-1 items-center justify-center text-sm text-surface-600-400">
			{loading ? 'Loading…' : 'Box not running. Press Start to bring up the screen.'}
		</div>
	{/if}
</div>
