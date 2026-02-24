import { randomUUID } from "node:crypto";
import { Pool } from "pg";
import type { CreditBalance, UsageEvent } from "../domain/types";

export type PersistentPaymentIntent = {
  intentId: string;
  userId: string;
  provider: "bkash" | "sslcommerz";
  bdtAmount: number;
  status: "initiated" | "credited" | "failed";
  mintedCredits: number;
};

export type PersistentUser = {
  userId: string;
  email: string;
  name?: string;
  passwordHash: string;
  createdAt: string;
};

export type PersistentApiKey = {
  key: string;
  userId: string;
  scopes: string[];
  revoked: boolean;
  createdAt: string;
};

export type PersistentAuthSession = {
  token: string;
  userId: string;
  provider: string;
  providerSubject?: string;
  providerEmail?: string;
  expiresAt: string;
  revoked: boolean;
  createdAt: string;
};

export type PersistentUserTwoFactor = {
  userId: string;
  secret: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
};

export class PostgresStore {
  private readonly pool: Pool;
  private schemaReady?: Promise<void>;

  constructor(connectionString: string) {
    this.pool = new Pool({ connectionString });
  }

  private async ensureSchema(): Promise<void> {
    if (!this.schemaReady) {
      this.schemaReady = (async () => {
        await this.pool.query(`
          CREATE TABLE IF NOT EXISTS credit_accounts (
            user_id TEXT PRIMARY KEY,
            available_credits INTEGER NOT NULL DEFAULT 0,
            purchased_credits INTEGER NOT NULL DEFAULT 0,
            promo_credits INTEGER NOT NULL DEFAULT 0,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS credit_ledger (
            id BIGSERIAL PRIMARY KEY,
            user_id TEXT NOT NULL,
            entry_type TEXT NOT NULL,
            credits INTEGER NOT NULL,
            reference_type TEXT NOT NULL,
            reference_id TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            UNIQUE(reference_type, reference_id)
          );

          CREATE TABLE IF NOT EXISTS usage_events (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            endpoint TEXT NOT NULL,
            model TEXT NOT NULL,
            credits INTEGER NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS payment_intents (
            intent_id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            provider TEXT NOT NULL,
            bdt_amount NUMERIC(12,2) NOT NULL,
            status TEXT NOT NULL,
            minted_credits INTEGER NOT NULL DEFAULT 0,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS payment_events (
            id BIGSERIAL PRIMARY KEY,
            event_key TEXT UNIQUE NOT NULL,
            intent_id TEXT NOT NULL,
            provider TEXT NOT NULL,
            provider_txn_id TEXT NOT NULL,
            verified BOOLEAN NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS users (
            user_id TEXT PRIMARY KEY,
            email TEXT UNIQUE NOT NULL,
            name TEXT,
            password_hash TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS api_keys (
            key TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            scopes TEXT[] NOT NULL,
            revoked BOOLEAN NOT NULL DEFAULT FALSE,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS permissions (
            permission_key TEXT PRIMARY KEY,
            description TEXT,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS roles (
            role_key TEXT PRIMARY KEY,
            description TEXT,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS role_permissions (
            role_key TEXT NOT NULL,
            permission_key TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY(role_key, permission_key)
          );

          CREATE TABLE IF NOT EXISTS user_roles (
            user_id TEXT NOT NULL,
            role_key TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY(user_id, role_key)
          );

          CREATE TABLE IF NOT EXISTS user_settings (
            user_id TEXT NOT NULL,
            setting_key TEXT NOT NULL,
            enabled BOOLEAN NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY(user_id, setting_key)
          );

          CREATE TABLE IF NOT EXISTS auth_sessions (
            token TEXT PRIMARY KEY,
            user_id TEXT,
            provider TEXT NOT NULL,
            provider_subject TEXT,
            provider_email TEXT,
            oauth_state TEXT UNIQUE,
            expires_at TIMESTAMPTZ NOT NULL,
            revoked BOOLEAN NOT NULL DEFAULT FALSE,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS user_2fa (
            user_id TEXT PRIMARY KEY,
            secret TEXT NOT NULL,
            enabled BOOLEAN NOT NULL DEFAULT FALSE,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );

          CREATE TABLE IF NOT EXISTS auth_2fa_challenges (
            challenge_id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            purpose TEXT NOT NULL,
            challenge_code TEXT NOT NULL,
            expires_at TIMESTAMPTZ NOT NULL,
            verified_at TIMESTAMPTZ,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
          );
        `);
      })();
    }

    await this.schemaReady;
  }

  async getBalance(userId: string): Promise<CreditBalance> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO credit_accounts (user_id) VALUES ($1) ON CONFLICT(user_id) DO NOTHING`,
      [userId],
    );

    const result = await this.pool.query(
      `SELECT user_id, available_credits, purchased_credits, promo_credits FROM credit_accounts WHERE user_id = $1`,
      [userId],
    );
    const row = result.rows[0];
    return {
      userId: row.user_id,
      availableCredits: Number(row.available_credits),
      purchasedCredits: Number(row.purchased_credits),
      promoCredits: Number(row.promo_credits),
    };
  }

  async consumeCredits(userId: string, credits: number, referenceId: string): Promise<boolean> {
    await this.ensureSchema();
    const client = await this.pool.connect();
    try {
      await client.query("BEGIN");
      await client.query(
        `INSERT INTO credit_accounts (user_id) VALUES ($1) ON CONFLICT(user_id) DO NOTHING`,
        [userId],
      );

      const update = await client.query(
        `UPDATE credit_accounts
         SET available_credits = available_credits - $2,
             purchased_credits = GREATEST(purchased_credits - $2, 0),
             updated_at = NOW()
         WHERE user_id = $1 AND available_credits >= $2`,
        [userId, credits],
      );

      if (update.rowCount !== 1) {
        await client.query("ROLLBACK");
        return false;
      }

      await client.query(
        `INSERT INTO credit_ledger (user_id, entry_type, credits, reference_type, reference_id)
         VALUES ($1, 'debit', $2, 'usage', $3)
         ON CONFLICT(reference_type, reference_id) DO NOTHING`,
        [userId, credits, referenceId],
      );

      await client.query("COMMIT");
      return true;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  async topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    await this.ensureSchema();
    const credits = Math.trunc(Math.max(0, bdtAmount) * 100);
    const client = await this.pool.connect();
    try {
      await client.query("BEGIN");
      await client.query(
        `INSERT INTO credit_accounts (user_id) VALUES ($1) ON CONFLICT(user_id) DO NOTHING`,
        [userId],
      );
      await client.query(
        `UPDATE credit_accounts
         SET available_credits = available_credits + $2,
             purchased_credits = purchased_credits + $2,
             updated_at = NOW()
         WHERE user_id = $1`,
        [userId, credits],
      );
      await client.query(
        `INSERT INTO credit_ledger (user_id, entry_type, credits, reference_type, reference_id)
         VALUES ($1, 'credit', $2, 'payment', $3)
         ON CONFLICT(reference_type, reference_id) DO NOTHING`,
        [userId, credits, referenceId],
      );
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }

    return this.getBalance(userId);
  }

  async addUsage(userId: string, endpoint: string, model: string, credits: number): Promise<UsageEvent> {
    await this.ensureSchema();
    const id = `usage_${randomUUID()}`;
    const result = await this.pool.query(
      `INSERT INTO usage_events (id, user_id, endpoint, model, credits)
       VALUES ($1, $2, $3, $4, $5)
       RETURNING id, user_id, endpoint, model, credits, created_at`,
      [id, userId, endpoint, model, credits],
    );
    const row = result.rows[0];
    return {
      id: row.id,
      userId: row.user_id,
      endpoint: row.endpoint,
      model: row.model,
      credits: Number(row.credits),
      createdAt: row.created_at.toISOString(),
    };
  }

  async listUsage(userId: string): Promise<UsageEvent[]> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT id, user_id, endpoint, model, credits, created_at
       FROM usage_events WHERE user_id = $1 ORDER BY created_at DESC LIMIT 500`,
      [userId],
    );
    return result.rows.map((row: Record<string, unknown>) => ({
      id: String(row.id),
      userId: String(row.user_id),
      endpoint: String(row.endpoint),
      model: String(row.model),
      credits: Number(row.credits),
      createdAt: (row.created_at as Date).toISOString(),
    }));
  }

  async createPaymentIntent(intent: PersistentPaymentIntent): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO payment_intents (intent_id, user_id, provider, bdt_amount, status, minted_credits)
       VALUES ($1, $2, $3, $4, $5, $6)`,
      [intent.intentId, intent.userId, intent.provider, intent.bdtAmount, intent.status, intent.mintedCredits],
    );
  }

  async getPaymentIntent(intentId: string): Promise<PersistentPaymentIntent | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT intent_id, user_id, provider, bdt_amount, status, minted_credits FROM payment_intents WHERE intent_id = $1`,
      [intentId],
    );
    const row = result.rows[0];
    if (!row) {
      return undefined;
    }
    return {
      intentId: row.intent_id,
      userId: row.user_id,
      provider: row.provider,
      bdtAmount: Number(row.bdt_amount),
      status: row.status,
      mintedCredits: Number(row.minted_credits),
    };
  }

  async recordPaymentEvent(
    eventKey: string,
    intentId: string,
    provider: string,
    providerTxnId: string,
    verified: boolean,
  ): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `INSERT INTO payment_events (event_key, intent_id, provider, provider_txn_id, verified)
       VALUES ($1, $2, $3, $4, $5)
       ON CONFLICT(event_key) DO NOTHING`,
      [eventKey, intentId, provider, providerTxnId, verified],
    );
    return result.rowCount === 1;
  }

  async markPaymentCredited(intentId: string, mintedCredits: number, status: "credited" | "failed"): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `UPDATE payment_intents SET status = $2, minted_credits = $3 WHERE intent_id = $1`,
      [intentId, status, mintedCredits],
    );
  }

  async createUser(input: { userId: string; email: string; name?: string; passwordHash: string }): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO users (user_id, email, name, password_hash)
       VALUES ($1, $2, $3, $4)`,
      [input.userId, input.email.toLowerCase(), input.name ?? null, input.passwordHash],
    );
  }

  async findUserByEmail(email: string): Promise<PersistentUser | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT user_id, email, name, password_hash, created_at FROM users WHERE email = $1`,
      [email.toLowerCase()],
    );
    const row = result.rows[0];
    if (!row) {
      return undefined;
    }
    return {
      userId: row.user_id,
      email: row.email,
      name: row.name ?? undefined,
      passwordHash: row.password_hash,
      createdAt: row.created_at.toISOString(),
    };
  }

  async findUserById(userId: string): Promise<PersistentUser | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT user_id, email, name, password_hash, created_at FROM users WHERE user_id = $1`,
      [userId],
    );
    const row = result.rows[0];
    if (!row) {
      return undefined;
    }
    return {
      userId: row.user_id,
      email: row.email,
      name: row.name ?? undefined,
      passwordHash: row.password_hash,
      createdAt: row.created_at.toISOString(),
    };
  }

  async createApiKey(input: { key: string; userId: string; scopes: string[] }): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO api_keys (key, user_id, scopes, revoked)
       VALUES ($1, $2, $3, FALSE)`,
      [input.key, input.userId, input.scopes],
    );
  }

  async validateApiKey(key: string, requiredScope: string): Promise<string | null> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT user_id, scopes, revoked FROM api_keys WHERE key = $1`,
      [key],
    );
    const row = result.rows[0];
    if (!row || row.revoked) {
      return null;
    }
    const scopes = Array.isArray(row.scopes) ? (row.scopes as string[]) : [];
    if (!scopes.includes(requiredScope)) {
      return null;
    }
    return row.user_id;
  }

  async listApiKeys(userId: string): Promise<PersistentApiKey[]> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT key, user_id, scopes, revoked, created_at FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`,
      [userId],
    );
    return result.rows.map((row) => ({
      key: row.key,
      userId: row.user_id,
      scopes: row.scopes ?? [],
      revoked: Boolean(row.revoked),
      createdAt: row.created_at.toISOString(),
    }));
  }

  async revokeApiKey(key: string, userId: string): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `UPDATE api_keys SET revoked = TRUE WHERE key = $1 AND user_id = $2 AND revoked = FALSE`,
      [key, userId],
    );
    return result.rowCount === 1;
  }

  async getApiKey(key: string): Promise<PersistentApiKey | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT key, user_id, scopes, revoked, created_at FROM api_keys WHERE key = $1`,
      [key],
    );
    const row = result.rows[0];
    if (!row) {
      return undefined;
    }
    return {
      key: row.key,
      userId: row.user_id,
      scopes: row.scopes ?? [],
      revoked: Boolean(row.revoked),
      createdAt: row.created_at.toISOString(),
    };
  }

  async upsertPermission(permissionKey: string, description?: string): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO permissions (permission_key, description)
       VALUES ($1, $2)
       ON CONFLICT(permission_key) DO UPDATE SET description = EXCLUDED.description`,
      [permissionKey, description ?? null],
    );
  }

  async upsertRole(roleKey: string, description?: string): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO roles (role_key, description)
       VALUES ($1, $2)
       ON CONFLICT(role_key) DO UPDATE SET description = EXCLUDED.description`,
      [roleKey, description ?? null],
    );
  }

  async assignRolePermission(roleKey: string, permissionKey: string): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO role_permissions (role_key, permission_key)
       VALUES ($1, $2)
       ON CONFLICT(role_key, permission_key) DO NOTHING`,
      [roleKey, permissionKey],
    );
  }

  async assignUserRole(userId: string, roleKey: string): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO user_roles (user_id, role_key)
       VALUES ($1, $2)
       ON CONFLICT(user_id, role_key) DO NOTHING`,
      [userId, roleKey],
    );
  }

  async listPermissionsForUser(userId: string): Promise<string[]> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT DISTINCT rp.permission_key
       FROM user_roles ur
       JOIN role_permissions rp ON rp.role_key = ur.role_key
       WHERE ur.user_id = $1`,
      [userId],
    );
    return result.rows.map((row) => String(row.permission_key));
  }

  async upsertUserSetting(userId: string, settingKey: string, enabled: boolean): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO user_settings (user_id, setting_key, enabled)
       VALUES ($1, $2, $3)
       ON CONFLICT(user_id, setting_key)
       DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
      [userId, settingKey, enabled],
    );
  }

  async getUserSettings(userId: string): Promise<Record<string, boolean>> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT setting_key, enabled FROM user_settings WHERE user_id = $1`,
      [userId],
    );
    return result.rows.reduce<Record<string, boolean>>((acc, row) => {
      acc[String(row.setting_key)] = Boolean(row.enabled);
      return acc;
    }, {});
  }

  async createAuthSession(input: {
    token: string;
    userId: string;
    provider: string;
    providerSubject?: string;
    providerEmail?: string;
    expiresAt: Date;
  }): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO auth_sessions (token, user_id, provider, provider_subject, provider_email, expires_at, revoked)
       VALUES ($1, $2, $3, $4, $5, $6, FALSE)`,
      [
        input.token,
        input.userId,
        input.provider,
        input.providerSubject ?? null,
        input.providerEmail ?? null,
        input.expiresAt,
      ],
    );
  }

  async findAuthSessionByToken(token: string): Promise<PersistentAuthSession | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT token, user_id, provider, provider_subject, provider_email, expires_at, revoked, created_at
       FROM auth_sessions
       WHERE token = $1`,
      [token],
    );
    const row = result.rows[0];
    if (!row || !row.user_id) {
      return undefined;
    }
    return {
      token: row.token,
      userId: row.user_id,
      provider: row.provider,
      providerSubject: row.provider_subject ?? undefined,
      providerEmail: row.provider_email ?? undefined,
      expiresAt: row.expires_at.toISOString(),
      revoked: Boolean(row.revoked),
      createdAt: row.created_at.toISOString(),
    };
  }

  async revokeAuthSession(token: string): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `UPDATE auth_sessions SET revoked = TRUE WHERE token = $1 AND revoked = FALSE`,
      [token],
    );
    return result.rowCount === 1;
  }

  async createOAuthState(state: string, expiresAt: Date): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO auth_sessions (token, provider, oauth_state, expires_at, revoked)
       VALUES ($1, 'google', $2, $3, FALSE)`,
      [`oauth_state_${state}`, state, expiresAt],
    );
  }

  async consumeOAuthState(state: string): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `UPDATE auth_sessions
       SET revoked = TRUE
       WHERE oauth_state = $1 AND revoked = FALSE AND expires_at > NOW()`,
      [state],
    );
    return result.rowCount === 1;
  }

  async upsertUserTwoFactor(input: { userId: string; secret: string; enabled: boolean }): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO user_2fa (user_id, secret, enabled)
       VALUES ($1, $2, $3)
       ON CONFLICT(user_id)
       DO UPDATE SET secret = EXCLUDED.secret, enabled = EXCLUDED.enabled, updated_at = NOW()`,
      [input.userId, input.secret, input.enabled],
    );
  }

  async getUserTwoFactor(userId: string): Promise<PersistentUserTwoFactor | undefined> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT user_id, secret, enabled, created_at, updated_at FROM user_2fa WHERE user_id = $1`,
      [userId],
    );
    const row = result.rows[0];
    if (!row) {
      return undefined;
    }
    return {
      userId: row.user_id,
      secret: row.secret,
      enabled: Boolean(row.enabled),
      createdAt: row.created_at.toISOString(),
      updatedAt: row.updated_at.toISOString(),
    };
  }

  async createTwoFactorChallenge(input: {
    challengeId: string;
    userId: string;
    purpose: string;
    challengeCode: string;
    expiresAt: Date;
  }): Promise<void> {
    await this.ensureSchema();
    await this.pool.query(
      `INSERT INTO auth_2fa_challenges (challenge_id, user_id, purpose, challenge_code, expires_at)
       VALUES ($1, $2, $3, $4, $5)`,
      [input.challengeId, input.userId, input.purpose, input.challengeCode, input.expiresAt],
    );
  }

  async verifyTwoFactorChallenge(challengeId: string, challengeCode: string): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `UPDATE auth_2fa_challenges
       SET verified_at = NOW()
       WHERE challenge_id = $1
         AND challenge_code = $2
         AND verified_at IS NULL
         AND expires_at > NOW()`,
      [challengeId, challengeCode],
    );
    return result.rowCount === 1;
  }

  async hasVerifiedTwoFactorChallenge(
    userId: string,
    challengeId: string,
    withinMinutes: number,
  ): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.pool.query(
      `SELECT challenge_id
       FROM auth_2fa_challenges
       WHERE challenge_id = $1
         AND user_id = $2
         AND verified_at IS NOT NULL
         AND verified_at > NOW() - make_interval(mins => $3)`,
      [challengeId, userId, withinMinutes],
    );
    return result.rowCount === 1;
  }
}
