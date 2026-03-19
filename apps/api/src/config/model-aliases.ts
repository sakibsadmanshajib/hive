/**
 * Static alias map: legacy/alternate OpenAI model names -> Hive model IDs.
 * Models that already exist as first-class IDs (gpt-4o, gpt-4o-mini) are NOT aliased.
 * Unknown model names pass through unchanged (no breaking change).
 */
export const MODEL_ALIASES: Record<string, string> = {
  'gpt-3.5-turbo': 'gpt-4o-mini',
  'gpt-4': 'gpt-4o',
  'gpt-4-turbo': 'gpt-4o',
  'text-embedding-ada-002': 'openai/text-embedding-3-small',
};

export function resolveModelAlias(modelId: string): string {
  return MODEL_ALIASES[modelId] ?? modelId;
}
