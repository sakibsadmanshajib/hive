import unittest

from app.ledger import CreditLedger
from app.payments import PaymentService


class PaymentsTests(unittest.TestCase):
    def test_duplicate_callback_does_not_duplicate_credits(self) -> None:
        ledger = CreditLedger()
        service = PaymentService(ledger)

        service.create_intent("intent-1", user_id="user-1", bdt_amount=100)
        service.handle_verified_event(
            provider="bkash",
            provider_txn_id="bkash-abc",
            intent_id="intent-1",
            verified=True,
        )
        service.handle_verified_event(
            provider="bkash",
            provider_txn_id="bkash-abc",
            intent_id="intent-1",
            verified=True,
        )

        self.assertEqual(ledger.balance("user-1").total, 10000)

    def test_unverified_event_never_mints_credits(self) -> None:
        ledger = CreditLedger()
        service = PaymentService(ledger)

        service.create_intent("intent-2", user_id="user-2", bdt_amount=100)
        service.handle_verified_event(
            provider="sslcommerz",
            provider_txn_id="ssl-abc",
            intent_id="intent-2",
            verified=False,
        )

        self.assertEqual(ledger.balance("user-2").total, 0)


if __name__ == "__main__":
    unittest.main()
