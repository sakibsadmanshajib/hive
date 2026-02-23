import { randomBytes } from "node:crypto";

interface ApiKeyRecord {
  userId: string;
  scopes: Set<string>;
  revoked: boolean;
}

export class ApiKeyService {
  private readonly store = new Map<string, ApiKeyRecord>();

  issueKey(userId: string, scopes: string[]): string {
    const key = randomBytes(32).toString("base64url");
    this.store.set(key, {
      userId,
      scopes: new Set(scopes),
      revoked: false,
    });
    return key;
  }

  validateKey(key: string, requiredScope: string): string | null {
    const record = this.store.get(key);
    if (record === undefined) {
      return null;
    }
    if (record.revoked) {
      return null;
    }
    if (!record.scopes.has(requiredScope)) {
      return null;
    }
    return record.userId;
  }

  revokeKey(key: string): void {
    const record = this.store.get(key);
    if (record === undefined) {
      return;
    }
    record.revoked = true;
  }
}
