import { createHmac } from "node:crypto";
import { describe, expect, it } from "vitest";

import {
  verifyBkashSignature,
  verifyHmacSha256Signature,
  verifyProviderSignature,
  verifySslcommerzSignature,
} from "../../src/domain/webhook-signatures";

describe("webhook signatures", () => {
  it("verifies valid hmac sha256 signature", () => {
    const body = '{"transaction_id":"tx-1"}';
    const secret = "test-secret";
    const signature = createHmac("sha256", secret).update(body, "utf8").digest("hex");

    expect(verifyHmacSha256Signature(signature, body, secret)).toBe(true);
  });

  it("fails hmac verification with invalid signature", () => {
    expect(verifyHmacSha256Signature("bad-signature", '{"transaction_id":"tx-1"}', "test-secret")).toBe(
      false,
    );
  });

  it("fails provider verification for replay window", () => {
    const body = '{"transaction_id":"tx-2"}';
    const secret = "test-secret";
    const headers = {
      "X-Signature": createHmac("sha256", secret).update(body, "utf8").digest("hex"),
      "X-Timestamp": "100",
    };

    expect(verifyProviderSignature(headers, body, secret, { now: 1000, toleranceSeconds: 300 })).toBe(false);
  });

  it("verifies bKash signature with valid headers", () => {
    const body = '{"paymentID":"p-1"}';
    const secret = "bkash-secret";
    const headers = {
      "X-BKash-Signature": createHmac("sha256", secret).update(body, "utf8").digest("hex"),
      "X-BKash-Timestamp": "1700000000",
    };

    expect(verifyBkashSignature(headers, body, secret, 1700000010)).toBe(true);
  });

  it("verifies sslcommerz canonical hash deterministically", () => {
    const payload = { amount: "100.00", currency: "BDT", tran_id: "T-1" };
    const canonical = "amount=100.00|currency=BDT|tran_id=T-1";
    const secret = "ssl-secret";
    const expectedHash = createHmac("sha256", secret).update(canonical, "utf8").digest("hex");

    expect(verifySslcommerzSignature(payload, expectedHash, secret)).toBe(true);
    expect(verifySslcommerzSignature(payload, "wrong-hash", secret)).toBe(false);
  });
});
