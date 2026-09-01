/**
 * Deep links from the console into Hive Chat, the forked Open WebUI front end
 * on its own origin.
 *
 * The chat consumes `?model=<id>` natively (vendor/open-webui
 * src/lib/components/chat/Chat.svelte): an id it knows is preselected as the
 * conversation model, an unknown id opens the model selector prefilled with
 * that text, so the link never dead-ends even if catalog and chat drift apart.
 *
 * NEXT_PUBLIC_CHAT_URL exists for laptop dev against a local chat instance;
 * unset, the default matches the deployed topology. Trailing slash stripped
 * so a pasted override like "http://localhost:8080/" doesn't produce a
 * double slash in the built URL.
 */
const CHAT_ORIGIN = (
  process.env.NEXT_PUBLIC_CHAT_URL ?? "https://chat-hive.scubed.co"
).replace(/\/+$/, "");

/** URL of a new chat with `modelId` preselected. */
export function chatModelUrl(modelId: string): string {
  return `${CHAT_ORIGIN}/?model=${encodeURIComponent(modelId)}`;
}

/**
 * Whether a catalog row can actually serve a chat completion.
 *
 * The catalog carries embedding, speech-to-text and text-to-speech aliases
 * (hive-embedding-default, hive-stt, hive-tts) alongside the chat ones, and a
 * Try in chat link on one of those drops a prospect into a chat window whose
 * first send fails (issue #1647).
 *
 * Gated on the badge the row itself declares rather than on a list of model
 * ids: public.model_aliases.capability_badges carries "chat" on every
 * chat-capable alias seeded to date (20260822_02_catalog_alias_restructure.sql,
 * 20260824_02_free_pool_router.sql, 20260830_03_free_pool_capability_truth.sql)
 * and on none of the others (20260717_02_voice_groq_stt_tts.sql carries voice,
 * stt and tts; 20260423_01_embedding_alias.sql carries embeddings). An alias
 * added later is then judged by what it declares, with no console-side list to
 * keep in step.
 */
export function isChatCapable(capabilityBadges: readonly string[]): boolean {
  return capabilityBadges.some(
    (badge) => badge.trim().toLowerCase() === "chat",
  );
}
