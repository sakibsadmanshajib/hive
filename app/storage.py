from __future__ import annotations

import sqlite3
import threading
from datetime import datetime, timezone
from typing import Any, Dict


class SQLiteStore:
    def __init__(self, db_path: str) -> None:
        self._conn = sqlite3.connect(db_path, check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._lock = threading.Lock()
        self._initialize_tables()

    def close(self) -> None:
        self._conn.close()

    def _initialize_tables(self) -> None:
        with self._lock, self._conn:
            self._conn.execute(
                """
                CREATE TABLE IF NOT EXISTS payment_intents (
                    intent_id TEXT PRIMARY KEY,
                    user_id TEXT NOT NULL,
                    bdt_amount REAL NOT NULL,
                    status TEXT NOT NULL,
                    created_at TEXT NOT NULL
                )
                """
            )
            self._conn.execute(
                """
                CREATE TABLE IF NOT EXISTS payment_events (
                    event_id TEXT PRIMARY KEY,
                    intent_id TEXT NOT NULL,
                    provider TEXT NOT NULL,
                    provider_txn_id TEXT NOT NULL,
                    verified INTEGER NOT NULL,
                    created_at TEXT NOT NULL,
                    UNIQUE(provider, provider_txn_id)
                )
                """
            )
            self._conn.execute(
                """
                CREATE TABLE IF NOT EXISTS credit_ledger (
                    entry_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id TEXT NOT NULL,
                    entry_type TEXT NOT NULL,
                    amount INTEGER NOT NULL,
                    reference_type TEXT NOT NULL,
                    reference_id TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    UNIQUE(reference_type, reference_id)
                )
                """
            )
            self._conn.execute(
                """
                CREATE TABLE IF NOT EXISTS usage_events (
                    request_id TEXT PRIMARY KEY,
                    user_id TEXT NOT NULL,
                    credits INTEGER NOT NULL,
                    model TEXT,
                    created_at TEXT NOT NULL
                )
                """
            )

    @staticmethod
    def _utc_now() -> str:
        return datetime.now(timezone.utc).isoformat()

    def record_payment_intent(
        self,
        intent_id: str,
        user_id: str,
        bdt_amount: float,
        status: str = "initiated",
    ) -> None:
        created_at = self._utc_now()
        with self._lock, self._conn:
            self._conn.execute(
                """
                INSERT INTO payment_intents(intent_id, user_id, bdt_amount, status, created_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(intent_id)
                DO UPDATE SET
                    user_id = excluded.user_id,
                    bdt_amount = excluded.bdt_amount,
                    status = excluded.status
                """,
                (intent_id, user_id, bdt_amount, status, created_at),
            )

    def record_payment_event(
        self,
        event_id: str,
        intent_id: str,
        provider: str,
        provider_txn_id: str,
        verified: bool,
    ) -> bool:
        with self._lock, self._conn:
            cursor = self._conn.execute(
                """
                INSERT OR IGNORE INTO payment_events(
                    event_id,
                    intent_id,
                    provider,
                    provider_txn_id,
                    verified,
                    created_at
                ) VALUES (?, ?, ?, ?, ?, ?)
                """,
                (event_id, intent_id, provider, provider_txn_id, int(verified), self._utc_now()),
            )
        return cursor.rowcount == 1

    def mark_intent_credited(self, intent_id: str, credits: int) -> bool:
        if credits <= 0:
            raise ValueError("credits must be positive")

        with self._lock, self._conn:
            row = self._conn.execute(
                "SELECT user_id FROM payment_intents WHERE intent_id = ?",
                (intent_id,),
            ).fetchone()
            if row is None:
                raise ValueError("payment intent not found")

            self._conn.execute(
                "UPDATE payment_intents SET status = ? WHERE intent_id = ?",
                ("credited", intent_id),
            )
            cursor = self._conn.execute(
                """
                INSERT OR IGNORE INTO credit_ledger(
                    user_id,
                    entry_type,
                    amount,
                    reference_type,
                    reference_id,
                    created_at
                ) VALUES (?, 'credit', ?, 'payment_intent', ?, ?)
                """,
                (row["user_id"], credits, intent_id, self._utc_now()),
            )
        return cursor.rowcount == 1

    def record_usage_event(self, request_id: str, user_id: str, credits: int, model: str | None = None) -> bool:
        if credits <= 0:
            raise ValueError("credits must be positive")

        with self._lock, self._conn:
            usage_cursor = self._conn.execute(
                """
                INSERT OR IGNORE INTO usage_events(request_id, user_id, credits, model, created_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (request_id, user_id, credits, model, self._utc_now()),
            )

            if usage_cursor.rowcount != 1:
                return False

            self._conn.execute(
                """
                INSERT INTO credit_ledger(
                    user_id,
                    entry_type,
                    amount,
                    reference_type,
                    reference_id,
                    created_at
                ) VALUES (?, 'debit', ?, 'usage_request', ?, ?)
                """,
                (user_id, credits, request_id, self._utc_now()),
            )
        return True

    def get_user_balance_summary(self, user_id: str) -> Dict[str, Any]:
        with self._lock:
            row = self._conn.execute(
                """
                SELECT
                    COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount END), 0) AS credited,
                    COALESCE(SUM(CASE WHEN entry_type = 'debit' THEN amount END), 0) AS debited
                FROM credit_ledger
                WHERE user_id = ?
                """,
                (user_id,),
            ).fetchone()

        credited = int(row["credited"])
        debited = int(row["debited"])
        return {
            "user_id": user_id,
            "credited": credited,
            "debited": debited,
            "balance": credited - debited,
        }
