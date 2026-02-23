import { randomUUID } from "node:crypto";

type LangfuseConfig = {
  enabled: boolean;
  baseUrl: string;
  publicKey?: string;
  secretKey?: string;
};

export type TracePayload = {
  userId: string;
  model: string;
  provider: string;
  endpoint: string;
  credits: number;
  promptPreview: string;
};

export class LangfuseClient {
  constructor(private readonly config: LangfuseConfig) {}

  isEnabled(): boolean {
    return this.config.enabled && Boolean(this.config.publicKey) && Boolean(this.config.secretKey);
  }

  async trace(payload: TracePayload): Promise<void> {
    if (!this.isEnabled()) {
      return;
    }

    const auth = Buffer.from(`${this.config.publicKey}:${this.config.secretKey}`).toString("base64");
    try {
      await fetch(`${this.config.baseUrl}/api/public/ingestion`, {
        method: "POST",
        headers: {
          authorization: `Basic ${auth}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          batch: [
            {
              id: `evt_${randomUUID()}`,
              type: "trace-create",
              timestamp: new Date().toISOString(),
              body: {
                id: `trc_${randomUUID()}`,
                name: payload.endpoint,
                userId: payload.userId,
                metadata: {
                  model: payload.model,
                  provider: payload.provider,
                  credits: payload.credits,
                  promptPreview: payload.promptPreview,
                },
              },
            },
          ],
        }),
      });
    } catch {
      // non-blocking telemetry
    }
  }
}
