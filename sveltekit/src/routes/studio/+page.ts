import { redirect } from '@sveltejs/kit';
import pb from '$lib/pocketbase';
import { buildLoginUrl } from '$lib/utils/redirect';
import type { PageLoad } from './$types';

// RequireAuth (not admin) — the OBS surface picker + spectator-key minter.
// Anyone signed in can browse the catalog; minting keys / copying live OBS links is gated
// in-page by canManageOverlays (admin/superuser or the overlay_manager role),
// with the backend as the real gate.
export const load: PageLoad = async ({ url, parent }) => {
	await parent();
	if (!pb.authStore.isValid) {
		throw redirect(303, buildLoginUrl(url.pathname + url.search));
	}
	return { requiresAuth: true };
};
