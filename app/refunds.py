from __future__ import annotations

from dataclasses import dataclass

from app.ledger import CreditLedger


@dataclass
class RefundQuote:
    refundable_credits: int
    refund_bdt: float


class RefundPolicy:
    def __init__(self, ledger: CreditLedger, window_days: int = 30) -> None:
        self.ledger = ledger
        self.window_days = window_days

    def quote(self, user_id: str) -> RefundQuote:
        credits = self.ledger.refundable_purchased_credits(user_id, within_days=self.window_days)
        refund_bdt = (credits / 100.0) * 0.9
        return RefundQuote(refundable_credits=credits, refund_bdt=round(refund_bdt, 4))
