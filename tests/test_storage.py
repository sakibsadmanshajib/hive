import os
import sqlite3
import tempfile
import threading
import unittest

from app.storage import SQLiteStore


class SQLiteStoreTests(unittest.TestCase):
    def setUp(self) -> None:
        fd, self.db_path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        self.store = SQLiteStore(self.db_path)

    def tearDown(self) -> None:
        self.store.close()
        if os.path.exists(self.db_path):
            os.remove(self.db_path)

    def test_initializes_expected_tables(self) -> None:
        with sqlite3.connect(self.db_path) as conn:
            rows = conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()

        table_names = {name for (name,) in rows}
        self.assertIn("payment_intents", table_names)
        self.assertIn("payment_events", table_names)
        self.assertIn("credit_ledger", table_names)
        self.assertIn("usage_events", table_names)

    def test_marking_intent_credited_is_idempotent(self) -> None:
        self.store.record_payment_intent(
            intent_id="intent-1",
            user_id="user-1",
            bdt_amount=125.0,
        )

        first = self.store.mark_intent_credited(intent_id="intent-1", credits=12500)
        second = self.store.mark_intent_credited(intent_id="intent-1", credits=12500)

        self.assertTrue(first)
        self.assertFalse(second)

        with sqlite3.connect(self.db_path) as conn:
            status = conn.execute(
                "SELECT status FROM payment_intents WHERE intent_id = ?",
                ("intent-1",),
            ).fetchone()
            entries = conn.execute(
                "SELECT COUNT(*) FROM credit_ledger WHERE reference_type = ? AND reference_id = ?",
                ("payment_intent", "intent-1"),
            ).fetchone()

        self.assertEqual(status[0], "credited")
        self.assertEqual(entries[0], 1)

    def test_usage_event_affects_ledger_balance_summary(self) -> None:
        self.store.record_payment_intent(
            intent_id="intent-2",
            user_id="user-2",
            bdt_amount=50.0,
        )
        self.store.mark_intent_credited(intent_id="intent-2", credits=5000)

        recorded = self.store.record_usage_event(
            request_id="req-1",
            user_id="user-2",
            credits=700,
            model="fast-chat",
        )

        self.assertTrue(recorded)
        summary = self.store.get_user_balance_summary("user-2")
        self.assertEqual(summary["credited"], 5000)
        self.assertEqual(summary["debited"], 700)
        self.assertEqual(summary["balance"], 4300)

    def test_store_allows_access_from_another_thread(self) -> None:
        self.store.record_payment_intent(
            intent_id="intent-thread",
            user_id="user-thread",
            bdt_amount=50.0,
        )

        result: dict[str, str] = {}

        def worker() -> None:
            try:
                self.store.record_payment_event(
                    event_id="thread-event",
                    intent_id="intent-thread",
                    provider="bkash",
                    provider_txn_id="bkash-thread",
                    verified=True,
                )
                result["status"] = "ok"
            except Exception as exc:  # noqa: BLE001
                result["status"] = str(exc)

        thread = threading.Thread(target=worker)
        thread.start()
        thread.join()

        self.assertEqual(result["status"], "ok")


if __name__ == "__main__":
    unittest.main()
