<script lang="ts">
	// Settings → Stream: the nameplate (live plate preview + motto + curated
	// banner picker) and the gamertag list. The preview IS the overlay's
	// NamePlate component at h=64, fed exactly what the broadcast gets: the
	// DEFAULT gamertag (the moderated on-air handle — not the display name),
	// the site avatar in the well, the motto line, and the picked banner under
	// the navy scrim. Banners come from the organizer's Selectable pool only —
	// players pick, they never upload.
	import { LoaderIcon, PlusIcon, TagIcon, Trash2Icon } from '@lucide/svelte';
	import pb from '$lib/pocketbase';
	import { auth } from '$lib/stores/auth.svelte';
	import { toastPromise } from '$lib/stores/toaster';
	import { apiBaseURL } from '$lib/utils/api-base';
	import Card from '$lib/components/ui/Card.svelte';
	import ActionBar from '$lib/components/settings/ActionBar.svelte';
	import NamePlate from '$lib/overlay/NamePlate.svelte';
	// Same faces as the OBS routes (additive CSS), so the preview measures true.
	import '$lib/styles/overlay-base.css';
	import { GAMERTAG_MAX_LEN } from '$lib/utils/gamertag';
	import type { MeGamertag } from '$lib/utils/identity';

	interface NameplateOption {
		id: string;
		name: string;
		art: string;
	}

	let {
		active,
		gamertags,
		defaultGamertagId,
		defaultTag,
		motto = $bindable(),
		nameplateId = $bindable(),
		savedMotto,
		savedNameplateId,
		busy,
		onsave,
		onreloadIdentity
	}: {
		active: boolean;
		gamertags: MeGamertag[];
		defaultGamertagId: string | null;
		defaultTag: string;
		motto: string;
		nameplateId: string;
		savedMotto: string;
		savedNameplateId: string;
		busy: boolean;
		onsave: () => void;
		onreloadIdentity: () => Promise<void>;
	} = $props();

	// ── Banner pool (organizer-curated, selectable only) ────────────────────
	let plates = $state<NameplateOption[]>([]);
	let platesLoaded = $state(false);
	$effect(() => {
		if (!active || platesLoaded) return;
		platesLoaded = true;
		void pb
			.collection('nameplates')
			.getFullList<{ id: string; name: string; art: string; selectable: boolean }>({
				sort: 'name'
			})
			.then((rows) => {
				// A worn-but-hidden banner stays offered to ITS wearer so re-saving
				// other fields doesn't silently strip it.
				plates = rows
					.filter((r) => r.art && (r.selectable || r.id === savedNameplateId))
					.map((r) => ({ id: r.id, name: r.name, art: r.art }));
			})
			.catch(() => {
				plates = [];
			});
	});

	function artURL(p: NameplateOption): string {
		return `${apiBaseURL()}/api/files/nameplates/${p.id}/${p.art}`;
	}
	const selectedPlate = $derived(plates.find((p) => p.id === nameplateId) ?? null);
	const bannerName = $derived(nameplateId === '' ? 'None' : (selectedPlate?.name ?? '…'));

	const avatarURL = $derived.by(() => {
		const u = auth.user;
		if (!u?.avatar) return '';
		return `${apiBaseURL()}/api/files/users/${u.id}/${u.avatar}?thumb=100x100`;
	});

	const dirty = $derived(motto !== savedMotto || nameplateId !== savedNameplateId);

	// ── Gamertags ───────────────────────────────────────────────────────────
	// Blocked names vanish from this list (per the design) — ping an admin.
	const visibleTags = $derived(gamertags.filter((g) => g.status !== 'blocked'));
	let newTag = $state('');
	let addingTag = $state(false);
	let tagBusyId = $state('');

	async function addGamertag() {
		const tag = newTag.trim();
		if (!auth.user || !tag) return;
		addingTag = true;
		try {
			await toastPromise(pb.collection('gamertags').create({ user: auth.user.id, tag }), {
				loading: { title: 'Adding gamertag' },
				success: { title: 'Added', description: tag },
				errorTitle: 'Add failed',
				errorDescription: (err) => {
					const m = err instanceof Error ? err.message : 'Failed';
					return m.toLowerCase().includes('unique') ? 'You already own that gamertag.' : m;
				}
			});
			newTag = '';
			await onreloadIdentity();
		} catch {
			/* toast shown */
		} finally {
			addingTag = false;
		}
	}

	async function removeGamertag(g: MeGamertag) {
		if (!auth.user) return;
		tagBusyId = g.id;
		try {
			await toastPromise(pb.collection('gamertags').delete(g.id), {
				loading: { title: 'Removing' },
				success: { title: 'Removed', description: g.tag },
				errorTitle: 'Remove failed'
			});
			await onreloadIdentity();
		} catch {
			/* toast shown */
		} finally {
			tagBusyId = '';
		}
	}

	async function setDefault(g: MeGamertag) {
		if (!auth.user) return;
		tagBusyId = g.id;
		try {
			await toastPromise(pb.collection('users').update(auth.user.id, { default_gamertag: g.id }), {
				loading: { title: 'Setting default' },
				success: { title: 'Default updated', description: `${g.tag} — profile saves regenerate.` },
				errorTitle: 'Update failed'
			});
			await onreloadIdentity();
		} catch {
			/* toast shown */
		} finally {
			tagBusyId = '';
		}
	}
</script>

{#if active}
	<div class="grid grid-cols-[repeat(auto-fit,minmax(min(440px,100%),1fr))] items-start gap-5">
		<!-- Nameplate -->
		<Card class="flex flex-col gap-4">
			<div class="flex flex-wrap items-center justify-between gap-2.5">
				<span class="text-[10px] font-bold tracking-[0.2em] text-surface-500 uppercase">
					Nameplate
				</span>
				<span class="text-[11.5px] opacity-60">How you appear on stream.</span>
			</div>

			<div
				class="flex flex-col items-center gap-2.5 overflow-x-auto rounded-[10px] bg-black/20 px-4 py-5.5"
			>
				<NamePlate
					player={{ display: defaultTag || 'Your gamertag', motto, avatar: avatarURL }}
					h={64}
					bg={selectedPlate ? artURL(selectedPlate) : ''}
				/>
				<span class="text-center text-[11px] opacity-50">
					One plate, every graphic — the POV bar, leaderboard, and post-game all scale this exact
					art.
				</span>
			</div>

			<label class="label">
				<span class="label-text">Motto</span>
				<input
					class="input"
					maxlength={40}
					placeholder="A little trash talk under your name…"
					bind:value={motto}
				/>
				<span class="text-[11px] opacity-60">
					Big plates only — long lines shrink to fit, then trim.
				</span>
			</label>

			<div class="flex flex-col gap-1.5">
				<span class="flex justify-between gap-2">
					<span class="label-text text-sm">Background</span>
					<span class="text-xs font-semibold opacity-50">{bannerName}</span>
				</span>
				<div
					class="grid max-h-54 grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2 overflow-x-hidden overflow-y-auto overscroll-contain rounded-[9px] bg-black/25 p-2"
				>
					<button
						type="button"
						title="None"
						aria-pressed={nameplateId === ''}
						class="flex aspect-6/1 min-w-0 items-center justify-center rounded-full bg-gradient-to-b from-[rgba(30,38,64,0.9)] to-[rgba(13,17,33,0.92)] text-[11px] font-semibold text-surface-600-400
							{nameplateId === ''
							? 'outline-2 outline-offset-2 outline-primary-500'
							: 'border border-surface-500/25'}"
						onclick={() => (nameplateId = '')}
					>
						None
					</button>
					{#each plates as p (p.id)}
						<button
							type="button"
							title={p.name}
							aria-pressed={nameplateId === p.id}
							class="aspect-6/1 min-w-0 rounded-full bg-cover bg-center
								{nameplateId === p.id
								? 'outline-2 outline-offset-2 outline-primary-500'
								: 'border border-surface-500/25'}"
							style="background-image:url({artURL(p)})"
							onclick={() => (nameplateId = p.id)}
						></button>
					{/each}
					{#if plates.length === 0 && platesLoaded}
						<span class="col-span-full p-2 text-xs opacity-50">
							No banners in the pool yet — organizers curate them under Organizer → Nameplates.
						</span>
					{/if}
				</div>
				<span class="text-[11px] opacity-60">
					Art sits under a navy scrim so your name stays readable — the plate itself never
					team-tints.
				</span>
			</div>
		</Card>

		<!-- Gamertags -->
		<Card class="flex flex-col gap-3">
			<div class="flex flex-wrap items-center justify-between gap-2.5">
				<span class="text-[10px] font-bold tracking-[0.2em] text-surface-500 uppercase">
					Gamertags
				</span>
				<span class="text-[11.5px] opacity-60">Every name here gets matched to live games.</span>
			</div>

			<form
				class="flex items-center gap-2"
				onsubmit={(e) => {
					e.preventDefault();
					void addGamertag();
				}}
			>
				<input
					class="input min-w-0 flex-1 font-mono text-[12.5px]"
					maxlength={GAMERTAG_MAX_LEN}
					placeholder="Add a name you play under…"
					bind:value={newTag}
					disabled={addingTag}
				/>
				<button
					type="submit"
					class="btn flex-none preset-tonal btn-sm"
					disabled={addingTag || !newTag.trim()}
				>
					{#if addingTag}<LoaderIcon class="size-4 animate-spin" />{:else}<PlusIcon
							class="size-4"
						/>{/if}
					<span>Add</span>
				</button>
			</form>

			{#each visibleTags as g (g.id)}
				{@const isDefault = g.id === defaultGamertagId}
				<div
					class="flex flex-wrap items-center gap-2.5 rounded-[9px] border px-3.5 py-2.5
						{isDefault ? 'border-primary-500/35' : 'border-surface-500/20'}"
				>
					<TagIcon class="size-3.5 flex-none opacity-50" />
					<span class="font-mono text-[13px] font-bold">{g.tag}</span>
					{#if g.status === 'pending'}
						<span
							class="rounded-md border border-surface-500/30 bg-surface-500/12 px-2 py-0.5 text-[10px] font-bold tracking-[0.14em] text-surface-600-400 uppercase"
						>
							Pending
						</span>
					{/if}
					{#if isDefault}
						<span
							class="rounded-md border border-primary-500/30 bg-primary-500/15 px-2 py-0.5 text-[10px] font-bold tracking-[0.14em] text-primary-600-400 uppercase"
						>
							Default
						</span>
					{/if}
					<span class="ml-auto inline-flex items-center gap-2">
						{#if !isDefault}
							<button
								class="btn preset-tonal btn-sm"
								onclick={() => setDefault(g)}
								disabled={tagBusyId === g.id}
							>
								{#if tagBusyId === g.id}<LoaderIcon class="size-3.5 animate-spin" />{/if}
								<span>Set default</span>
							</button>
						{/if}
						<button
							class="btn-icon opacity-50 btn-sm hover:opacity-100"
							title="Remove gamertag"
							onclick={() => removeGamertag(g)}
							disabled={tagBusyId === g.id}
						>
							<Trash2Icon class="size-3.5" />
						</button>
					</span>
				</div>
			{:else}
				<p class="text-sm opacity-50">No gamertags yet — add the names you play under.</p>
			{/each}

			<span class="text-[11.5px] opacity-70">
				Your <b class="text-primary-600-400">default</b> is the name the Halo: CE and Halo 2
				profiles carry — changing it regenerates both saves. {GAMERTAG_MAX_LEN} characters max — that's
				all the save holds.
			</span>
			<span class="text-[11px] opacity-60">
				Blocked names vanish from this list — ping an admin on the Discord if yours does.
			</span>
		</Card>

		<div class="col-span-full">
			<ActionBar note="Nameplate + gamertags go live on every overlay when you save.">
				<button class="btn preset-filled" onclick={onsave} disabled={busy || !dirty}>
					{#if busy}<LoaderIcon class="size-4 animate-spin" />{/if}
					<span>Save changes</span>
				</button>
			</ActionBar>
		</div>
	</div>
{/if}
