import type { CatalogModel } from "@/lib/control-plane/client";

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
 *  2. Any Hive routing alias. Hive's own aliases are the documented entry
 *     points, and an upstream model id happening to sort first would read as a
 *     recommendation to bypass them.
 *  3. Whatever the catalog listed first, then the alias seeded by
 *     supabase/migrations, so the snippets stay runnable even when the catalog
 *     could not be read at all.
 */
export function pickQuickstartAlias(models: CatalogModel[]): string {
  const isHiveAlias = (model: CatalogModel) => model.id.startsWith("hive-");
  return (
    models.find(
      (model) =>
        isHiveAlias(model) &&
        model.pricing.pricing_mode === "fixed" &&
        model.capability_badges.includes("chat"),
    )?.id ??
    models.find(isHiveAlias)?.id ??
    models[0]?.id ??
    "hive-default"
  );
}
