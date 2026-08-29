import type { CatalogModel } from "@/lib/control-plane/client";

/** Placeholder used when no plaintext key is in hand at render time. */
export const QUICKSTART_KEY_VARIABLE = "$HIVE_API_KEY";

export interface QuickstartCurlInput {
  /** Already resolved by `resolveApiBaseUrl`, so no trailing slash. */
  baseUrl: string;
  /** Alias from `pickQuickstartAlias`. */
  model: string;
  /**
   * The plaintext key, when the caller is holding one. Omitted, the command
   * names the shell variable instead.
   *
   * Shell quoted like every other interpolation here. Nothing on our side runs
   * this string, but the reader pastes it into their own shell, so a value
   * carrying a quote has to land there as text and not as syntax.
   */
  credential?: string;
}

/**
 * The one place the quickstart curl is composed.
 *
 * Both callers show it at the moment it is most useful and are otherwise
 * unrelated screens: `/console/docs` (documentation, no key in hand) and the
 * created-key panel on `/console/api-keys` (issue #550, key in hand exactly
 * once). Two copies of this string would drift, and a drifted example is the
 * defect this function exists to prevent, not a cosmetic one: a developer's
 * first request either works or the product looks broken.
 */
export function buildQuickstartCurl({
  baseUrl,
  model,
  credential,
}: QuickstartCurlInput): string {
  const payload = JSON.stringify(
    {
      model,
      messages: [{ role: "user", content: "Say hello in one sentence." }],
    },
    null,
    2,
  );

  // The credential branch is the one place a shell expansion is wanted, so it
  // is the one place double quotes are used. Everything else is single quoted,
  // which is what makes the model id safe: it arrives from the catalog, and a
  // catalog row is admin managed rather than a constant, so a value carrying a
  // quote must end up as text in the reader's shell rather than as syntax.
  const authHeader =
    credential === undefined
      ? `-H "Authorization: Bearer ${QUICKSTART_KEY_VARIABLE}"`
      : `-H ${shellQuote(`Authorization: Bearer ${credential}`)}`;

  return `curl ${shellQuote(`${baseUrl}/chat/completions`)} \\
  ${authHeader} \\
  -H 'Content-Type: application/json' \\
  -d ${shellQuote(payload)}`;
}

/**
 * POSIX single-quoting: everything inside is literal, and the only character
 * that cannot appear is the single quote itself, which is closed, escaped and
 * reopened. Safe for every byte, including newlines, which is why the
 * multi-line JSON payload survives it unchanged.
 */
function shellQuote(value: string): string {
  return `'${value.split("'").join(`'\\''`)}'`;
}

/**
 * pickQuickstartAlias chooses the model id the API quickstart snippets name.
 *
 * The snippets are the first request a developer ever sends, usually by paste,
 * so the alias has to be one that actually answers on a fresh account.
 *
 * Order of preference, and why each rung exists:
 *
 *  1. A fixed-price Hive routing alias that speaks chat. Fixed price is the
 *     load-bearing half: a variable-price alias is settled from the cost its
 *     router reports after the fact, so it takes a much larger up-front credit
 *     hold, and a balance that would cover thousands of real requests can still
 *     fail to cover that hold. Issue #1372 is exactly that, arriving through
 *     exactly this pick: the catalog's first `hive-` id was the variable-price
 *     router, so the quickstart handed every new developer a call that refused.
 *     The chat capability check keeps an embeddings-only alias out of a
 *     /chat/completions snippet, which is the other way a first-id-wins pick
 *     produces a sample that cannot run.
 *  2. Any Hive routing alias that speaks chat. Hive's own aliases are the
 *     documented entry points, and an upstream model id happening to sort first
 *     would read as a recommendation to bypass them. The chat check repeats
 *     here rather than only on rung 1: without it, a catalog that listed
 *     hive-embedding-default ahead of every chat alias would put an
 *     embeddings-only id into a /chat/completions snippet, which is the same
 *     class of unrunnable quickstart this whole function exists to stop.
 *  3. The alias seeded by supabase/migrations, so the snippets stay runnable
 *     even when the catalog could not be read at all.
 *
 * There used to be a rung between 2 and 3: whatever the catalog listed first,
 * `models[0]?.id`. Its removal is a fix, not a simplification. Rungs 1 and 2
 * already require a `hive-` prefix, so that rung was reachable only when the
 * catalog held no Hive chat alias at all, and what it returned then was an
 * upstream model id. Upstream ids name the provider (`openai/...`, `groq/...`),
 * and this repository's standing rule is that provider names never reach a
 * customer-facing surface. It put one into a command printed under a freshly
 * minted key. The seeded alias is both provider-blind and likelier to route.
 */
export function pickQuickstartAlias(models: CatalogModel[]): string {
  const speaksChat = (model: CatalogModel) =>
    model.id.startsWith("hive-") && model.capability_badges.includes("chat");
  return (
    models.find(
      (model) => speaksChat(model) && model.pricing.pricing_mode === "fixed",
    )?.id ??
    models.find(speaksChat)?.id ??
    "hive-default"
  );
}
