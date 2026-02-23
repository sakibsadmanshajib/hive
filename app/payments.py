from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Set

from app.ledger import CreditLedger
from app.storage import SQLiteStore


@dataclass
class PaymentIntent:
    intent_id: str
    user_id: str
    bdt_amount: float
    status: str
    minted_credits: int = 0


class PaymentService:
    def __init__(self, ledger: CreditLedger, store: SQLiteStore | None = None) -> None:
        self.ledger = ledger
        self.store = store
        self.intents: Dict[str, PaymentIntent] = {}
        self._processed_provider_txns: Set[str] = set()

    def create_intent(self, intent_id: str, user_id: str, bdt_amount: float) -> PaymentIntent:
        intent = PaymentIntent(intent_id=intent_id, user_id=user_id, bdt_amount=bdt_amount, status="initiated")
        self.intents[intent_id] = intent
        if self.store is not None:
            self.store.record_payment_intent(intent_id, user_id=user_id, bdt_amount=bdt_amount, status="initiated")
        return intent

    def handle_verified_event(self, provider: str, provider_txn_id: str, intent_id: str, verified: bool) -> None:
        event_key = f"{provider}:{provider_txn_id}"
        if event_key in self._processed_provider_txns:
            return
        self._processed_provider_txns.add(event_key)

        if self.store is not None:
            self.store.record_payment_event(
                event_id=event_key,
                intent_id=intent_id,
                provider=provider,
                provider_txn_id=provider_txn_id,
                verified=verified,
            )

        intent = self.intents.get(intent_id)
        if intent is None:
            return
        if not verified:
            intent.status = "failed"
            return
        if intent.status == "credited":
            return

        credits = int(intent.bdt_amount * 100)
        self.ledger.mint_purchased(intent.user_id, credits, payment_id=intent.intent_id)
        if self.store is not None:
            self.store.mark_intent_credited(intent.intent_id, credits)
        intent.status = "credited"
        intent.minted_credits = credits
