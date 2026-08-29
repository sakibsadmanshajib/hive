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
