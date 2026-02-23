import { createHmac, timingSafeEqual } from "node:crypto";

export interface ProviderSignatureOptions {
  signatureHeader?: string;
  timestampHeader?: string;
  toleranceSeconds?: number;
  now?: number;
}

function toBodyBuffer(body: Uint8Array | string): Buffer {
  if (typeof body === "string") {
    return Buffer.from(body, "utf8");
  }
  return Buffer.from(body);
}

function safeCompareHex(left: string, right: string): boolean {
  if (left.length !== right.length) {
    return false;
  }
  return timingSafeEqual(Buffer.from(left, "utf8"), Buffer.from(right, "utf8"));
}

export function verifyHmacSha256Signature(signature: string, body: Uint8Array | string, secret: string): boolean {
  const expected = createHmac("sha256", Buffer.from(secret, "utf8")).update(toBodyBuffer(body)).digest("hex");
  return safeCompareHex(signature, expected);
}

export function isTimestampWithinTolerance(
  timestamp: string | number,
  options?: { now?: number; toleranceSeconds?: number },
): boolean {
  const timestampValue = Number.parseInt(String(timestamp), 10);
  if (Number.isNaN(timestampValue)) {
    return false;
  }

  const now = options?.now ?? Math.trunc(Date.now() / 1000);
  const toleranceSeconds = options?.toleranceSeconds ?? 300;
  return Math.abs(now - timestampValue) <= toleranceSeconds;
}

export function verifyProviderSignature(
  headers: Record<string, string>,
  body: Uint8Array | string,
  secret: string,
  options?: ProviderSignatureOptions,
): boolean {
  const signatureHeader = options?.signatureHeader ?? "X-Signature";
  const timestampHeader = options?.timestampHeader ?? "X-Timestamp";
  const signature = headers[signatureHeader];
  const timestamp = headers[timestampHeader];
  if (!signature || timestamp === undefined) {
    return false;
  }

  if (!verifyHmacSha256Signature(signature, body, secret)) {
    return false;
  }

  return isTimestampWithinTolerance(timestamp, {
    now: options?.now,
    toleranceSeconds: options?.toleranceSeconds,
  });
}

export function verifyBkashSignature(
  headers: Record<string, string>,
  body: Uint8Array | string,
  secret: string,
  now?: number,
): boolean {
  return verifyProviderSignature(headers, body, secret, {
    signatureHeader: "X-BKash-Signature",
    timestampHeader: "X-BKash-Timestamp",
    now,
  });
}

export function verifySslcommerzSignature(payload: Record<string, string>, expectedHash: string, secret: string): boolean {
  const canonical = Object.keys(payload)
    .sort()
    .map((key) => `${key}=${payload[key]}`)
    .join("|");
  const digest = createHmac("sha256", Buffer.from(secret, "utf8")).update(canonical, "utf8").digest("hex");
  return safeCompareHex(digest, expectedHash);
}
