import { describe, expect, it } from 'vitest';

import { displayNameFromEmail, resolveDisplayName } from './displayName';

describe('displayNameFromEmail', () => {
	it('capitalizes a dotted local part', () => {
		expect(displayNameFromEmail('sakib.shajib@example.com')).toBe('Sakib Shajib');
	});

	it('treats underscore and hyphen as word separators too', () => {
		expect(displayNameFromEmail('qa_tester@hive.test')).toBe('Qa Tester');
		expect(displayNameFromEmail('qa-tester@hive.test')).toBe('Qa Tester');
	});

	it('drops plus-addressing tags', () => {
		expect(displayNameFromEmail('sakib+demo@example.com')).toBe('Sakib');
	});

	it('strips a quoted local part', () => {
		expect(displayNameFromEmail('"sakib"@example.com')).toBe('Sakib');
	});

	it('returns empty for an empty or whitespace-only address', () => {
		expect(displayNameFromEmail('')).toBe('');
		expect(displayNameFromEmail('   ')).toBe('');
	});

	it('never throws and never returns empty for a non-empty address', () => {
		expect(displayNameFromEmail('...@example.com')).toBe('User');
		expect(() => displayNameFromEmail(undefined as unknown as string)).not.toThrow();
	});

	it('caps the derived name at 64 characters', () => {
		const long = `${'a'.repeat(80)}@example.com`;
		expect(displayNameFromEmail(long).length).toBeLessThanOrEqual(64);
	});

	it('strips bidirectional override characters rather than rendering them', () => {
		// U+202E RIGHT-TO-LEFT OVERRIDE
		const hostile = `sakib‮evil@example.com`;
		expect(displayNameFromEmail(hostile)).not.toContain('‮');
	});
});

describe('resolveDisplayName', () => {
	it('keeps a real display name as-is', () => {
		expect(resolveDisplayName('Sakib Shajib', 'sakib@example.com')).toBe('Sakib Shajib');
	});

	it('strips bidirectional-override characters from an accepted server name', () => {
		const hostile = 'Sakib‮evil';
		const result = resolveDisplayName(hostile, 'sakib@example.com');
		expect(result).not.toContain('‮');
		expect(result).toBe('Sakibevil');
	});

	it('never returns the raw email address when name is empty', () => {
		const result = resolveDisplayName('', 'qa-tester@hive.test');
		expect(result).not.toBe('qa-tester@hive.test');
		expect(result).toBe('Qa Tester');
	});

	it('never returns the raw email address when name equals the email', () => {
		// The exact regression: an account whose `name` column was written as
		// its own email address, either before the OAuth signup patch shipped
		// or via the local password signup path the patch never touches.
		const result = resolveDisplayName('qa-tester@hive.test', 'qa-tester@hive.test');
		expect(result).not.toBe('qa-tester@hive.test');
		expect(result).toBe('Qa Tester');
	});

	it('derives from an email-shaped name even when it does not match the email field', () => {
		const result = resolveDisplayName('someone@other.example', 'qa-tester@hive.test');
		expect(result).not.toContain('@');
	});

	it('falls back to "User" when both name and email are empty', () => {
		expect(resolveDisplayName('', '')).toBe('User');
		expect(resolveDisplayName(null, undefined)).toBe('User');
	});

	it('never throws on null or undefined input', () => {
		expect(() => resolveDisplayName(null, null)).not.toThrow();
		expect(() => resolveDisplayName(undefined, undefined)).not.toThrow();
	});
});
