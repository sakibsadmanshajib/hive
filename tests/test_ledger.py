import unittest

from app.ledger import CreditLedger


class CreditLedgerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.ledger = CreditLedger()
        self.user_id = "user-1"
        self.ledger.mint_purchased(self.user_id, 1000, payment_id="pay-1")

    def test_reserve_then_settle_with_refund_delta(self) -> None:
        reservation_id = self.ledger.reserve(self.user_id, request_id="req-1", estimated_credits=250)
        self.ledger.settle(reservation_id=reservation_id, actual_credits=180)

        balance = self.ledger.balance(self.user_id)
        self.assertEqual(balance.total, 820)
        self.assertEqual(balance.reserved, 0)
        self.assertEqual(balance.available, 820)

    def test_reserve_fails_when_insufficient(self) -> None:
        with self.assertRaises(ValueError):
            self.ledger.reserve(self.user_id, request_id="req-2", estimated_credits=4000)


if __name__ == "__main__":
    unittest.main()
