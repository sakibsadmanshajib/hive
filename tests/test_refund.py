import unittest
from datetime import datetime, timedelta, timezone

from app.ledger import CreditLedger
from app.refunds import RefundPolicy


class RefundPolicyTests(unittest.TestCase):
    def test_refund_uses_30_day_window_and_90_percent_rate(self) -> None:
        ledger = CreditLedger(now_fn=lambda: datetime(2026, 2, 23, tzinfo=timezone.utc))
        user_id = "user-1"
        recent = datetime(2026, 2, 10, tzinfo=timezone.utc)
        old = datetime(2025, 12, 1, tzinfo=timezone.utc)

        ledger.mint_purchased(user_id, 1000, payment_id="recent-pay", created_at=recent)
        ledger.mint_purchased(user_id, 1000, payment_id="old-pay", created_at=old)
        ledger.consume(user_id, request_id="req-1", credits=400)
        ledger.mint_promo(user_id, 500, campaign_id="eid")

        policy = RefundPolicy(ledger)
        quote = policy.quote(user_id)

        self.assertEqual(quote.refundable_credits, 600)
        self.assertAlmostEqual(quote.refund_bdt, 5.4, places=3)


if __name__ == "__main__":
    unittest.main()
