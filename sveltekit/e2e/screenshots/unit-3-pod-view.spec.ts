import { test } from '@playwright/test';
import { loginAsAdmin } from '../helpers/auth';
import { installPodMocks, MOCK_INSTANCE_LIVE } from '../helpers/mocks';
import { screenshotPodRoute } from '../helpers/screenshot';

test('unit-3-pod-view: /admin/pod/[name]/view/ renders screen + controller + start/stop', async ({
	page
}) => {
	await installPodMocks(page);
	await loginAsAdmin(page);

	await screenshotPodRoute(page, `/admin/pod/${MOCK_INSTANCE_LIVE}/view/`, 'unit-3-pod-view');
});
