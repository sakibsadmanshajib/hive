/**
 * Resolve an untrusted `return_to` form value into a safe redirect target.
 *
 * Only console paths on the given origin are honoured, so a crafted value
 * cannot turn a post-and-redirect route into a redirect gadget.
 *
 * The value is resolved against the origin *before* it is checked, because a
 * raw prefix test on the submitted string is bypassable: "/console/../settings"
 * starts with "/console" but normalizes to "/settings". Comparing the parsed
 * origin also rejects absolute and protocol-relative targets
 * ("//evil.test/x" parses with a different origin), and returning only pathname
 * plus search keeps any fragment or embedded credentials out of the Location
 * header.
 *
 * Percent-encoded separators ("%2F") are deliberately left encoded: they stay a
 * literal segment inside /console rather than escaping it, so at worst they
 * resolve to a 404 instead of a different area of the app.
 */
export function resolveReturnTo(
  value: FormDataEntryValue | null,
  origin: string,
): string {
  if (typeof value !== "string" || value === "") return "/console";

  let url: URL;
  let base: URL;
  try {
    base = new URL(origin);
    url = new URL(value, base);
  } catch {
    return "/console";
  }

  if (url.origin !== base.origin) return "/console";
  if (url.pathname !== "/console" && !url.pathname.startsWith("/console/")) {
    return "/console";
  }

  return `${url.pathname}${url.search}`;
}
