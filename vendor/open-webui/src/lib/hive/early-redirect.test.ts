import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The fast exit lives as an inline script in app.html because its entire point
// is to run before the bundler's output exists. These tests therefore execute
// the shipped bytes themselves: the script block is pulled out of app.html and
// run against stubbed browser globals. That keeps the inline copy from drifting
// away from the behaviour these assertions pin down.

const appHtmlPath = resolve(__dirname, '../../app.html');
const appHtml = readFileSync(appHtmlPath, 'utf-8');

// Locate the block by its marker comment, then slice out the first <script>
// body after it. Index arithmetic instead of one clever regex.
const marker = appHtml.indexOf('signed-out fast exit');
if (marker === -1) {
	throw new Error('early SSO comment marker not found in app.html');
}
const segment = appHtml.slice(marker);
const open = segment.indexOf('<script>');
const close = segment.indexOf('</script>', open);
if (open === -1 || close === -1) {
	throw new Error('early SSO script not found in app.html');
}
const scriptSource = segment.slice(open + '<script>'.length, close);

type Store = Map<string, string>;

const flush = () => new Promise((resolveDone) => setTimeout(resolveDone, 0));

describe('app.html signed-out fast exit', () => {
	let location: { pathname: string; hostname: string; replace: ReturnType<typeof vi.fn> };
	let localStorageStore: Store;
	let sessionStorageStore: Store;
	let sessionStorageBroken: boolean;
	let headChildren: Array<{ rel?: string; href?: string }>;
	let fetchMock: ReturnType<typeof vi.fn>;

	const config = (overrides: Record<string, unknown> = {}) => ({
		oauth: {
			auto_redirect: true,
			providers: { oidc: {} }
		},
		features: {
			auth: true,
			enable_login_form: false
		},
		onboarding: false,
		...overrides
	});

	const runScript = async ({
		pathname = '/',
		hostname = 'chat-hive.scubed.co',
		localStorageToken = null,
		tokenCookie = null,
		session = {},
		cfg = config(),
		fetchImpl
	}: {
		pathname?: string;
		hostname?: string;
		localStorageToken?: string | null;
		tokenCookie?: string | null;
		session?: Record<string, string>;
		cfg?: Record<string, unknown> | 'reject' | 'http500';
		fetchImpl?: ReturnType<typeof vi.fn>;
	} = {}) => {
		location = {
			pathname,
			hostname,
			replace: vi.fn()
		};
		localStorageStore = new Map();
		if (localStorageToken) {
			localStorageStore.set('token', localStorageToken);
		}
		sessionStorageStore = new Map(Object.entries(session));
		sessionStorageBroken = false;
		headChildren = [];
		fetchMock = fetchImpl ?? vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(cfg)
		});

		const globals = {
			window: {} as Record<string, unknown>,
			location,
			document: {
				cookie: tokenCookie ?? '',
				head: {
					appendChild: (node: { rel?: string; href?: string }) => {
						headChildren.push(node);
					}
				},
				createElement: () => ({}) as { rel?: string; href?: string }
			},
			fetch: fetchMock,
			Date,
			localStorage: {
				getItem: (key: string) => localStorageStore.get(key) ?? null,
				setItem: (key: string, value: string) => void localStorageStore.set(key, value),
				removeItem: (key: string) => void localStorageStore.delete(key)
			},
			sessionStorage: {
				getItem: (key: string) => {
					if (sessionStorageBroken) throw new Error('storage unavailable');
					return sessionStorageStore.get(key) ?? null;
				},
				setItem: (key: string, value: string) => {
					if (sessionStorageBroken) throw new Error('storage unavailable');
					void sessionStorageStore.set(key, value);
				},
				removeItem: (key: string) => {
					if (sessionStorageBroken) throw new Error('storage unavailable');
					void sessionStorageStore.delete(key);
				}
			}
		};
		for (const [name, value] of Object.entries(globals)) {
			(globalThis as Record<string, unknown>)[name] = value;
		}

		new Function(scriptSource)();

		await flush();
		await flush();
	};

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('leaves immediately for the single provider when signed out', async () => {
		await runScript();
		expect(location.replace).toHaveBeenCalledWith('/oauth/oidc/login');
		expect(fetchMock).toHaveBeenCalledWith('/api/config', { credentials: 'same-origin' });
		expect(sessionStorageStore.has('hive:sso-attempt-at')).toBe(true);
	});

	it('preconnects to the console host on a scubed.co origin', async () => {
		await runScript();
		expect(headChildren).toEqual([{ rel: 'preconnect', href: 'https://console-hive.scubed.co' }]);
	});

	it('does not preconnect on other origins', async () => {
		await runScript({ hostname: 'chat.example.com' });
		expect(headChildren).toEqual([]);
	});

	it('stays when a localStorage token exists', async () => {
		await runScript({ localStorageToken: 'abc' });
		expect(location.replace).not.toHaveBeenCalled();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('stays when a token cookie exists', async () => {
		await runScript({ tokenCookie: 'token=abc; Path=/' });
		expect(location.replace).not.toHaveBeenCalled();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('stays on paths other than /', async () => {
		await runScript({ pathname: '/new-chat' });
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays when the signed-out marker is set', async () => {
		await runScript({ session: { 'hive:signed-out': '1' } });
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays within the retry window after a recorded attempt', async () => {
		await runScript({
			session: { 'hive:sso-attempt-at': String(Date.now() - 3000) },
			fetchImpl: undefined
		});
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('leaves again once the attempt stamp is older than the retry window', async () => {
		await runScript({ session: { 'hive:sso-attempt-at': String(Date.now() - 20000) } });
		expect(location.replace).toHaveBeenCalledWith('/oauth/oidc/login');
	});

	it('stays when auto redirect is off', async () => {
		await runScript({
			cfg: config({ oauth: { auto_redirect: false, providers: { oidc: {} } } })
		});
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays when more than one provider is configured', async () => {
		await runScript({
			cfg: config({ oauth: { auto_redirect: true, providers: { oidc: {}, saml: {} } } })
		});
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays when the login form is enabled', async () => {
		await runScript({ cfg: config({ features: { auth: true, enable_login_form: true } }) });
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays when auth is disabled', async () => {
		await runScript({ cfg: config({ features: { auth: false, enable_login_form: false } }) });
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('stays during onboarding', async () => {
		await runScript({ cfg: config({ onboarding: true }) });
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('fails closed when the attempt stamp cannot be stored', async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolveGate) => {
			release = resolveGate;
		});
		await runScript({
			fetchImpl: vi
				.fn()
				.mockReturnValue(gate.then(() => ({ ok: true, json: () => Promise.resolve(config()) })))
		});
		sessionStorageBroken = true;
		release();
		await flush();
		await flush();
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('fails closed when the config fetch rejects', async () => {
		await runScript({
			fetchImpl: vi.fn().mockRejectedValue(new Error('network down'))
		});
		expect(location.replace).not.toHaveBeenCalled();
	});

	it('ignores a non-ok config response', async () => {
		await runScript({
			fetchImpl: vi.fn().mockResolvedValue({ ok: false, json: () => Promise.resolve({}) })
		});
		expect(location.replace).not.toHaveBeenCalled();
	});
});
