import type { RawControl } from "./enumerate";

/**
 * Stable, human readable identity for a control.
 *
 * Used as the registry lookup key, the coverage report key, and the way a
 * control is re-located after a reload. It deliberately prefers a test id,
 * then an id, then the accessible name, then the href, so a cosmetic
 * reordering of the DOM does not invalidate a registry entry.
 */
export function controlKey(control: RawControl): string {
  const label =
    control.testid !== ""
      ? `testid=${control.testid}`
      : control.id !== ""
        ? `#${control.id}`
        : control.name !== ""
          ? control.name
          : control.href !== ""
            ? `href=${control.href}`
            : "(unnamed)";
  const role = control.role !== "" ? `[role=${control.role}]` : "";
  const suffix = control.ordinal > 0 ? `~${control.ordinal}` : "";
  return `${control.tag}${role}|${label}${suffix}`;
}
