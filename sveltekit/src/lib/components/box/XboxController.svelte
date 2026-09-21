<script lang="ts">
	import { RotateCcwIcon } from '@lucide/svelte';
	import { KEYSYM } from '$lib/utils/vnc-keyboard';

	let {
		disabled = false,
		onPress,
		onRelease,
		onReset,
		class: extraClass = ''
	}: {
		disabled?: boolean;
		onPress?: (sym: number) => void;
		onRelease?: (sym: number) => void;
		onReset?: () => void;
		class?: string;
	} = $props();

	let pressed = $state<Record<string, boolean>>({});

	function handlePointerDown(e: PointerEvent, key: string) {
		if (disabled) return;
		const sym = KEYSYM[key];
		if (sym == null) return;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		pressed = { ...pressed, [key]: true };
		onPress?.(sym);
	}

	function handlePointerUp(e: PointerEvent, key: string) {
		const sym = KEYSYM[key];
		if (sym == null) return;
		const target = e.currentTarget as HTMLElement;
		if (target.hasPointerCapture(e.pointerId)) target.releasePointerCapture(e.pointerId);
		if (!pressed[key]) return;
		pressed = { ...pressed, [key]: false };
		onRelease?.(sym);
	}

	type ButtonColor = 'red' | 'green' | 'blue' | 'yellow' | 'gray';
	// Tailwind JIT only generates classes it sees as literal strings, so each
	// (color, pressed) combination must be spelled out — no template literals.
	// Backed by theme-agnostic xbox-{a,b,x,y,controller} ramps from layout.css
	// so the controller stays consistent across all six Skeleton themes. The
	// semantic color names (red/green/blue/yellow/gray) are kept at call sites
	// since they describe what the player sees on a real Xbox controller.
	const BUTTON_CLASSES: Record<ButtonColor, { up: string; down: string }> = {
		red: {
			up: 'bg-xbox-b-900 border-2 border-xbox-b-500',
			down: 'bg-xbox-b-500 border-2 border-xbox-b-500'
		},
		green: {
			up: 'bg-xbox-a-900 border-2 border-xbox-a-500',
			down: 'bg-xbox-a-500 border-2 border-xbox-a-500'
		},
		blue: {
			up: 'bg-xbox-x-900 border-2 border-xbox-x-500',
			down: 'bg-xbox-x-500 border-2 border-xbox-x-500'
		},
		yellow: {
			up: 'bg-xbox-y-900 border-2 border-xbox-y-500',
			down: 'bg-xbox-y-500 border-2 border-xbox-y-500'
		},
		gray: {
			up: 'bg-xbox-controller-900 border-2 border-xbox-controller-500',
			down: 'bg-xbox-controller-500 border-2 border-xbox-controller-500'
		}
	};

	function buttonClass(key: string, color: ButtonColor): string {
		const c = BUTTON_CLASSES[color];
		return pressed[key] ? c.down : c.up;
	}
</script>

<div class="flex flex-col gap-3 select-none {extraClass}">
	<!-- Top bar: LB LT | Reset | RT RB -->
	<div class="flex items-center justify-between gap-2">
		<div class="flex gap-1">
			<button
				class="btn btn-sm {buttonClass('1', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, '1')}
				onpointerup={(e) => handlePointerUp(e, '1')}
				onpointercancel={(e) => handlePointerUp(e, '1')}
				onlostpointercapture={(e) => handlePointerUp(e, '1')}>LB</button
			>
			<button
				class="btn btn-sm {buttonClass('w', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'w')}
				onpointerup={(e) => handlePointerUp(e, 'w')}
				onpointercancel={(e) => handlePointerUp(e, 'w')}
				onlostpointercapture={(e) => handlePointerUp(e, 'w')}>LT</button
			>
		</div>
		<div class="flex gap-1">
			{#if onReset}
				<button
					type="button"
					class="btn preset-tonal-warning btn-sm"
					{disabled}
					onclick={onReset}
					title="Send Ctrl+R to xemu — soft reset to dashboard"
				>
					<RotateCcwIcon class="size-4" />
					<span>Reset</span>
				</button>
			{/if}
		</div>
		<div class="flex gap-1">
			<button
				class="btn btn-sm {buttonClass('o', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'o')}
				onpointerup={(e) => handlePointerUp(e, 'o')}
				onpointercancel={(e) => handlePointerUp(e, 'o')}
				onlostpointercapture={(e) => handlePointerUp(e, 'o')}>RT</button
			>
			<button
				class="btn btn-sm {buttonClass('2', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, '2')}
				onpointerup={(e) => handlePointerUp(e, '2')}
				onpointercancel={(e) => handlePointerUp(e, '2')}
				onlostpointercapture={(e) => handlePointerUp(e, '2')}>RB</button
			>
		</div>
	</div>

	<!-- Middle: D-pad ⇋ Face diamond -->
	<div class="flex items-center justify-between gap-2">
		<!-- D-pad -->
		<div class="grid grid-cols-3 grid-rows-3 gap-1">
			<div></div>
			<button
				class="btn aspect-square {buttonClass('Up', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'Up')}
				onpointerup={(e) => handlePointerUp(e, 'Up')}
				onpointercancel={(e) => handlePointerUp(e, 'Up')}
				onlostpointercapture={(e) => handlePointerUp(e, 'Up')}>↑</button
			>
			<div></div>
			<button
				class="btn aspect-square {buttonClass('Left', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'Left')}
				onpointerup={(e) => handlePointerUp(e, 'Left')}
				onpointercancel={(e) => handlePointerUp(e, 'Left')}
				onlostpointercapture={(e) => handlePointerUp(e, 'Left')}>←</button
			>
			<div></div>
			<button
				class="btn aspect-square {buttonClass('Right', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'Right')}
				onpointerup={(e) => handlePointerUp(e, 'Right')}
				onpointercancel={(e) => handlePointerUp(e, 'Right')}
				onlostpointercapture={(e) => handlePointerUp(e, 'Right')}>→</button
			>
			<div></div>
			<button
				class="btn aspect-square {buttonClass('Down', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'Down')}
				onpointerup={(e) => handlePointerUp(e, 'Down')}
				onpointercancel={(e) => handlePointerUp(e, 'Down')}
				onlostpointercapture={(e) => handlePointerUp(e, 'Down')}>↓</button
			>
			<div></div>
		</div>

		<!-- Face diamond (Y top, X left, B right, A bottom) -->
		<div class="grid grid-cols-3 grid-rows-3 gap-1">
			<div></div>
			<button
				class="btn aspect-square rounded-full {buttonClass('y', 'yellow')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'y')}
				onpointerup={(e) => handlePointerUp(e, 'y')}
				onpointercancel={(e) => handlePointerUp(e, 'y')}
				onlostpointercapture={(e) => handlePointerUp(e, 'y')}>Y</button
			>
			<div></div>
			<button
				class="btn aspect-square rounded-full {buttonClass('x', 'blue')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'x')}
				onpointerup={(e) => handlePointerUp(e, 'x')}
				onpointercancel={(e) => handlePointerUp(e, 'x')}
				onlostpointercapture={(e) => handlePointerUp(e, 'x')}>X</button
			>
			<div></div>
			<button
				class="btn aspect-square rounded-full {buttonClass('b', 'red')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'b')}
				onpointerup={(e) => handlePointerUp(e, 'b')}
				onpointercancel={(e) => handlePointerUp(e, 'b')}
				onlostpointercapture={(e) => handlePointerUp(e, 'b')}>B</button
			>
			<div></div>
			<button
				class="btn aspect-square rounded-full {buttonClass('a', 'green')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'a')}
				onpointerup={(e) => handlePointerUp(e, 'a')}
				onpointercancel={(e) => handlePointerUp(e, 'a')}
				onlostpointercapture={(e) => handlePointerUp(e, 'a')}>A</button
			>
			<div></div>
		</div>
	</div>

	<!-- Bottom: Left stick | Back Start | Right stick -->
	<div class="flex items-center justify-between gap-2">
		<div class="grid grid-cols-3 grid-rows-3 gap-1">
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('e', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'e')}
				onpointerup={(e) => handlePointerUp(e, 'e')}
				onpointercancel={(e) => handlePointerUp(e, 'e')}
				onlostpointercapture={(e) => handlePointerUp(e, 'e')}>L↑</button
			>
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('s', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 's')}
				onpointerup={(e) => handlePointerUp(e, 's')}
				onpointercancel={(e) => handlePointerUp(e, 's')}
				onlostpointercapture={(e) => handlePointerUp(e, 's')}>L←</button
			>
			<button
				class="btn aspect-square text-[10px] btn-sm {buttonClass('3', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, '3')}
				onpointerup={(e) => handlePointerUp(e, '3')}
				onpointercancel={(e) => handlePointerUp(e, '3')}
				onlostpointercapture={(e) => handlePointerUp(e, '3')}>L3</button
			>
			<button
				class="btn aspect-square btn-sm {buttonClass('f', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'f')}
				onpointerup={(e) => handlePointerUp(e, 'f')}
				onpointercancel={(e) => handlePointerUp(e, 'f')}
				onlostpointercapture={(e) => handlePointerUp(e, 'f')}>L→</button
			>
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('d', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'd')}
				onpointerup={(e) => handlePointerUp(e, 'd')}
				onpointercancel={(e) => handlePointerUp(e, 'd')}
				onlostpointercapture={(e) => handlePointerUp(e, 'd')}>L↓</button
			>
			<div></div>
		</div>

		<div class="flex gap-1">
			<button
				class="btn btn-sm {buttonClass('BackSpace', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'BackSpace')}
				onpointerup={(e) => handlePointerUp(e, 'BackSpace')}
				onpointercancel={(e) => handlePointerUp(e, 'BackSpace')}
				onlostpointercapture={(e) => handlePointerUp(e, 'BackSpace')}>Back</button
			>
			<button
				class="btn btn-sm {buttonClass('Return', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'Return')}
				onpointerup={(e) => handlePointerUp(e, 'Return')}
				onpointercancel={(e) => handlePointerUp(e, 'Return')}
				onlostpointercapture={(e) => handlePointerUp(e, 'Return')}>Start</button
			>
		</div>

		<div class="grid grid-cols-3 grid-rows-3 gap-1">
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('i', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'i')}
				onpointerup={(e) => handlePointerUp(e, 'i')}
				onpointercancel={(e) => handlePointerUp(e, 'i')}
				onlostpointercapture={(e) => handlePointerUp(e, 'i')}>R↑</button
			>
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('j', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'j')}
				onpointerup={(e) => handlePointerUp(e, 'j')}
				onpointercancel={(e) => handlePointerUp(e, 'j')}
				onlostpointercapture={(e) => handlePointerUp(e, 'j')}>R←</button
			>
			<button
				class="btn aspect-square text-[10px] btn-sm {buttonClass('4', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, '4')}
				onpointerup={(e) => handlePointerUp(e, '4')}
				onpointercancel={(e) => handlePointerUp(e, '4')}
				onlostpointercapture={(e) => handlePointerUp(e, '4')}>R3</button
			>
			<button
				class="btn aspect-square btn-sm {buttonClass('l', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'l')}
				onpointerup={(e) => handlePointerUp(e, 'l')}
				onpointercancel={(e) => handlePointerUp(e, 'l')}
				onlostpointercapture={(e) => handlePointerUp(e, 'l')}>R→</button
			>
			<div></div>
			<button
				class="btn aspect-square btn-sm {buttonClass('k', 'gray')}"
				{disabled}
				onpointerdown={(e) => handlePointerDown(e, 'k')}
				onpointerup={(e) => handlePointerUp(e, 'k')}
				onpointercancel={(e) => handlePointerUp(e, 'k')}
				onlostpointercapture={(e) => handlePointerUp(e, 'k')}>R↓</button
			>
			<div></div>
		</div>
	</div>
</div>
