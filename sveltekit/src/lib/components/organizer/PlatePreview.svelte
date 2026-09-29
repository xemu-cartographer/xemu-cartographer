<script lang="ts">
	// Width-responsive wrapper around the overlay's NamePlate (CL-18 geometry):
	// measures its own width and scales the 6:1 master to fill it, so library
	// cards and the crop-dialog chrome render the plate EXACTLY as the overlay
	// draws it. The sample identity mirrors the handoff mock.
	import NamePlate from '$lib/overlay/NamePlate.svelte';
	// The overlay's self-hosted variable faces (additive: @font-face + tokens
	// only), so the plate measures here exactly as the OBS routes render it.
	import '$lib/styles/overlay-base.css';

	let {
		bg = '',
		chromeOnly = false,
		name = 'Sgt Johnson',
		motto = 'settling it on the sticks'
	}: {
		bg?: string;
		chromeOnly?: boolean;
		name?: string;
		motto?: string;
	} = $props();

	let w = $state(0);
</script>

<div bind:clientWidth={w} class="w-full" style="aspect-ratio: 6 / 1">
	{#if w > 0}
		<NamePlate player={{ display: name, motto }} h={w / 6} {bg} {chromeOnly} />
	{/if}
</div>
