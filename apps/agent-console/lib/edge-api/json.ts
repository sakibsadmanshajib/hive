// Structural JSON decoding helpers, mirroring
// apps/web-console/lib/control-plane/client.ts: no `any`/`unknown` casts,
// every external payload is narrowed through explicit type guards before
// its fields are read.

export type JsonPrimitive = string | number | boolean | null;
export interface JsonObject {
  [key: string]: JsonValue;
}
export type JsonArray = JsonValue[];
export type JsonValue = JsonPrimitive | JsonObject | JsonArray;

export function isJsonObject(value: JsonValue | null): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseJsonValue(text: string): JsonValue | null {
  if (!text) {
    return null;
  }
  try {
    const parsed: JsonValue = JSON.parse(text);
    return parsed;
  } catch {
    return null;
  }
}

export function readStringField(source: JsonObject, key: string): string | null {
  const value = source[key];
  return typeof value === "string" ? value : null;
}

export function readBooleanField(source: JsonObject, key: string): boolean | null {
  const value = source[key];
  return typeof value === "boolean" ? value : null;
}

export function readObjectField(source: JsonObject, key: string): JsonObject | null {
  const value = source[key];
  return isJsonObject(value) ? value : null;
}

export function readArrayField(source: JsonObject, key: string): JsonArray | null {
  const value = source[key];
  return Array.isArray(value) ? value : null;
}

export async function readResponseText(response: Response): Promise<string> {
  try {
    return await response.text();
  } catch {
    return "";
  }
}
