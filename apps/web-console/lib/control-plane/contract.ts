/**
 * Wire-contract constants shared with the control-plane.
 *
 * Deliberately dependency-free. The natural home for these is
 * lib/control-plane/client.ts, next to the response types they belong to,
 * but that module imports next/headers and the server Supabase client, so a
 * value import from it drags a server-only dependency graph into whatever
 * imports it. lib/analytics/cache-metrics.ts is a pure module that must stay
 * free of that graph, and it needs this value at runtime rather than as a
 * type, so the constant lives here instead.
 */

/**
 * The group_key the control-plane reports for a summary row whose grouping
 * column is NULL, which today means only usage_events.api_key_id. It goes
 * NULL for three causes that the row itself cannot tell apart: a request
 * served without an API key at all (console and chat traffic), a request that
 * failed before a key was resolved, and a key deleted after the fact, since
 * the foreign key is ON DELETE SET NULL. Anything rendered for this bucket
 * has to be true of all three.
 *
 * Mirrors usage.UnattributedGroupKey in
 * apps/control-plane/internal/usage/repository.go (issue #1347). The two are
 * pinned to each other by TestUnattributedGroupKeyMatchesTheConsole in that
 * package, which reads this file, so a rename on either side fails there
 * rather than silently falling back to the deleted-key label here.
 *
 * It cannot collide with a real api key id, which renders as a UUID.
 */
export const UNATTRIBUTED_GROUP_KEY = "unattributed";
