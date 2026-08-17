import { describe, expect, it } from 'vitest';

import {
	SSO_RETRY_WINDOW_MS,
	ssoAutoRedirectDecision,
	type SsoRedirectConfig,
	type SsoRedirectContext
} from './sso-redirect';

// The shape of the demo deployment: one provider, no password form, no LDAP.
const ssoOnlyConfig: SsoRedirectConfig = {
	oauth: { auto_redirect: true, providers: { oidc: 'Hive' } },
	features: {
		auth: true,
		enable_login_form: false,
		enable_ldap: false,
		auth_trusted_header: false
	},
	onboarding: false
};

const ctx = (overrides: Partial<SsoRedirectContext> = {}): SsoRedirectContext => ({
	form: null,
	error: null,
	signedOut: false,
	hasSession: false,
	lastAttemptAt: null,
	now: 1_000_000,
	...overrides
});

describe('ssoAutoRedirectDecision', () => {
	it('sends a fresh visitor straight to the only provider', () => {
		expect(ssoAutoRedirectDecision(ssoOnlyConfig, ctx())).toEqual({
			redirect: true,
			provider: 'oidc'
		});
	});

	it('stays put until the deployment opts in', () => {
		const config = { ...ssoOnlyConfig, oauth: { auto_redirect: false, providers: { oidc: 'Hive' } } };
		expect(ssoAutoRedirectDecision(config, ctx())).toEqual({ redirect: false, reason: 'disabled' });
	});

	it('stays put when the configuration still presents a real choice', () => {
		const twoProviders: SsoRedirectConfig = {
			...ssoOnlyConfig,
			oauth: { auto_redirect: true, providers: { oidc: 'Hive', google: 'Google' } }
		};
		expect(ssoAutoRedirectDecision(twoProviders, ctx())).toEqual({
			redirect: false,
			reason: 'not-single-provider'
		});

		const withPasswordForm: SsoRedirectConfig = {
			...ssoOnlyConfig,
			features: { ...ssoOnlyConfig?.features, enable_login_form: true }
		};
		expect(ssoAutoRedirectDecision(withPasswordForm, ctx())).toEqual({
			redirect: false,
			reason: 'other-auth-mode'
		});

		const withLdap: SsoRedirectConfig = {
			...ssoOnlyConfig,
			features: { ...ssoOnlyConfig?.features, enable_ldap: true }
		};
		expect(ssoAutoRedirectDecision(withLdap, ctx())).toEqual({
			redirect: false,
			reason: 'other-auth-mode'
		});
	});

	it('never redirects out of the failure page it was sent to', () => {
		expect(ssoAutoRedirectDecision(ssoOnlyConfig, ctx({ error: 'Invalid credentials' }))).toEqual({
			redirect: false,
			reason: 'provider-error'
		});
	});

	it('honours the manual escape hatch', () => {
		expect(ssoAutoRedirectDecision(ssoOnlyConfig, ctx({ form: '1' }))).toEqual({
			redirect: false,
			reason: 'manual-requested'
		});
	});

	it('lets a user actually sign out instead of bouncing them back in', () => {
		// The provider session usually outlives ours, so a redirect here would
		// re-authenticate silently and signing out would be unreachable.
		expect(ssoAutoRedirectDecision(ssoOnlyConfig, ctx({ signedOut: true }))).toEqual({
			redirect: false,
			reason: 'signed-out'
		});
	});

	it('does not redirect a visitor who already has a session', () => {
		expect(ssoAutoRedirectDecision(ssoOnlyConfig, ctx({ hasSession: true }))).toEqual({
			redirect: false,
			reason: 'already-signed-in'
		});
	});

	it('does not redirect during first run onboarding', () => {
		expect(ssoAutoRedirectDecision({ ...ssoOnlyConfig, onboarding: true }, ctx())).toEqual({
			redirect: false,
			reason: 'onboarding'
		});
	});

	it('breaks the bounce loop when a round trip comes straight back empty', () => {
		// The dangerous case: the provider returns the browser here with no error
		// and no session. Without the guard the page redirects again, forever.
		const decision = ssoAutoRedirectDecision(
			ssoOnlyConfig,
			ctx({ lastAttemptAt: 1_000_000 - 2_000 })
		);
		expect(decision).toEqual({ redirect: false, reason: 'recent-attempt' });
	});

	it('allows a deliberate retry once the guard window has passed', () => {
		const decision = ssoAutoRedirectDecision(
			ssoOnlyConfig,
			ctx({ lastAttemptAt: 1_000_000 - SSO_RETRY_WINDOW_MS - 1 })
		);
		expect(decision).toEqual({ redirect: true, provider: 'oidc' });
	});

	it('ignores an attempt stamp from the future rather than locking the user out', () => {
		const decision = ssoAutoRedirectDecision(ssoOnlyConfig, ctx({ lastAttemptAt: 2_000_000 }));
		expect(decision).toEqual({ redirect: true, provider: 'oidc' });
	});

	it('does nothing at all without a configuration', () => {
		expect(ssoAutoRedirectDecision(null, ctx())).toEqual({ redirect: false, reason: 'disabled' });
	});
});
