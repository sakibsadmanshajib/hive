/*
 * How this app knows it is being rendered inside the Hive chat shell.
 *
 * Middleware reads the query the shell frames it with and puts the answer on
 * the request headers; the root layout reads them and sets the attributes on
 * <html> before anything paints. Kept in its own module so the layout does not
 * import middleware, which would drag the Supabase server client into a
 * component bundle for the sake of two strings.
 */

export const HIVE_EMBED_HEADER = "x-hive-embed";
export const HIVE_THEME_HEADER = "x-hive-theme";

export type HiveEmbedTheme = "light" | "dark";

/** Narrows an untrusted theme value to the two the stylesheet knows. */
export function readEmbedTheme(value: string | null): HiveEmbedTheme | undefined {
  return value === "dark" || value === "light" ? value : undefined;
}
