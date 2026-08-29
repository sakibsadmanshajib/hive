/**
 * Turning an api_key group key into something a person can read.
 *
 * `usage_events` stores `endpoint` and `model_alias` as human strings, so
 * grouping by either of those needs no resolution at all. `api_key_id` is
 * the one grouping dimension whose natural value is an opaque id, and the
 * control plane's analytics endpoints return it raw with no name beside it
 * (`SpendSummaryRow` carries `group_key`, `total_credits`, `entry_count` and
 * nothing else). Issue #1403: the Overview tile joined the key list client
 * side and rendered nicknames, while the Spend, Usage and Errors tabs
 * rendered the same ids as bare UUIDs in both the table and the chart axis.
 *
 * The join lives here so both surfaces resolve a key the same way.
 */
import type { ApiKey } from "@/lib/control-plane/client";
import { UNATTRIBUTED_GROUP_KEY } from "@/lib/control-plane/contract";

export interface ApiKeyGroupLabel {
  label: string;
  suffix: string;
}

export function apiKeysById(
  keys: readonly ApiKey[]
): ReadonlyMap<string, ApiKey> {
  return new Map(keys.map((key) => [key.id, key] as const));
}

/**
 * Resolve one group key. The unattributed bucket is matched before the
 * lookup because it mixes causes the row cannot tell apart (traffic that
 * carried no key, an error raised before a key was resolved, a key deleted
 * under ON DELETE SET NULL), so calling it a deleted key would be false for
 * two of the three (issue #1347).
 */
export function resolveApiKeyGroup(
  groupKey: string,
  keyById: ReadonlyMap<string, ApiKey>
): ApiKeyGroupLabel {
  if (groupKey === UNATTRIBUTED_GROUP_KEY) {
    return { label: "Unattributed", suffix: "no key on record" };
  }
  const key = keyById.get(groupKey);
  return {
    label: key ? key.nickname : "Deleted key",
    suffix: key?.redacted_suffix ?? groupKey.slice(0, 8),
  };
}

/**
 * How much of a nickname a table cell or a chart axis tick will show. The
 * nickname is bounded at the control plane now, but a key minted before that
 * bound existed can still carry thousands of characters, and both of the
 * surfaces this function feeds take the string raw with no truncation of
 * their own (issue #1400).
 */
const MAX_LABEL_CHARS = 40;

function clampLabel(label: string): string {
  const chars = [...label];
  if (chars.length <= MAX_LABEL_CHARS) return label;
  return `${chars.slice(0, MAX_LABEL_CHARS).join("")}…`;
}

/**
 * One-line form for a table cell or a chart axis tick, where the label and
 * the masked tail cannot occupy separate columns. The tail is what keeps two
 * keys that share a nickname apart.
 */
export function formatApiKeyGroup(
  groupKey: string,
  keyById: ReadonlyMap<string, ApiKey>
): string {
  const { label, suffix } = resolveApiKeyGroup(groupKey, keyById);
  if (groupKey === UNATTRIBUTED_GROUP_KEY) return label;
  return `${clampLabel(label)} (${suffix})`;
}
