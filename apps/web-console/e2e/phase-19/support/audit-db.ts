import { expect } from "@playwright/test";

/** One audit_log row, narrowed to the columns these specs assert on. */
export interface AuditRow {
  action: string;
  severity: string;
}

/**
 * Read the audit rows a spec's own request should have written.
 *
 * `since` anchors the scan to the request boundary: this runs against a shared
 * Supabase project, so an unrelated concurrent job's row for the same action
 * would otherwise satisfy the assertion.
 *
 * The `pg` import is dynamic because the module is installed by the workflow
 * for the run rather than carried as an app dependency. A missing module is a
 * failure, not a skip: the install step ran, so its absence means that step
 * broke, and skipping here would leave the spec reporting green while
 * asserting nothing about the audit trail.
 */
export async function auditRowsSince(
  dbURL: string,
  action: string,
  since: Date,
): Promise<AuditRow[]> {
  const pg = await import("pg").catch(() => null);
  expect(pg, "pg module not installed; the workflow's install step must run first").not.toBeNull();
  if (!pg) {
    return [];
  }

  const db = new pg.Client({ connectionString: dbURL });
  await db.connect();
  try {
    const result = await db.query<AuditRow>(
      `SELECT action, severity
         FROM public.audit_log
        WHERE ts >= $1
          AND action = $2`,
      [since, action],
    );
    return result.rows;
  } finally {
    await db.end();
  }
}
