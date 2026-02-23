import { verifyBkashSignature, verifySslcommerzSignature } from "../domain/webhook-signatures";

export type BkashAdapterConfig = {
  webhookSecret: string;
  verifyEndpoint?: string;
  bearerToken?: string;
};

export type SslcommerzAdapterConfig = {
  webhookSecret: string;
  validatorEndpoint?: string;
  storeId?: string;
  storePassword?: string;
};

export class BkashAdapter {
  constructor(private readonly config: BkashAdapterConfig) {}

  async verifyWebhook(headers: Record<string, string>, rawBody: string, providerTxnId: string): Promise<boolean> {
    const signatureOk = verifyBkashSignature(
      {
        "X-BKash-Signature": headers["x-bkash-signature"] ?? "",
        "X-BKash-Timestamp": headers["x-bkash-timestamp"] ?? "",
      },
      rawBody,
      this.config.webhookSecret,
    );
    if (!signatureOk) {
      return false;
    }

    if (!this.config.verifyEndpoint || !this.config.bearerToken) {
      return true;
    }

    const response = await fetch(this.config.verifyEndpoint, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${this.config.bearerToken}`,
      },
      body: JSON.stringify({ paymentID: providerTxnId }),
    });
    if (!response.ok) {
      return false;
    }
    const payload = (await response.json()) as { transactionStatus?: string; statusCode?: string };
    return payload.transactionStatus === "Completed" || payload.statusCode === "0000";
  }
}

export class SslcommerzAdapter {
  constructor(private readonly config: SslcommerzAdapterConfig) {}

  async verifyWebhook(payload: Record<string, unknown>, signature: string): Promise<boolean> {
    const canonicalPayload = {
      provider: String(payload.provider ?? ""),
      intent_id: String(payload.intent_id ?? ""),
      provider_txn_id: String(payload.provider_txn_id ?? ""),
      verified: String(payload.verified ?? false),
    };

    const localSignature = verifySslcommerzSignature(canonicalPayload, signature, this.config.webhookSecret);
    if (!localSignature) {
      return false;
    }

    if (!this.config.validatorEndpoint || !this.config.storeId || !this.config.storePassword) {
      return true;
    }

    const providerTxnId = String(payload.provider_txn_id ?? "");
    const url = new URL(this.config.validatorEndpoint);
    url.searchParams.set("val_id", providerTxnId);
    url.searchParams.set("store_id", this.config.storeId);
    url.searchParams.set("store_passwd", this.config.storePassword);
    url.searchParams.set("format", "json");

    const response = await fetch(url, { method: "GET" });
    if (!response.ok) {
      return false;
    }
    const data = (await response.json()) as { status?: string };
    return data.status === "VALID" || data.status === "VALIDATED";
  }
}
