from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Callable, Dict, List
from uuid import uuid4


@dataclass
class Balance:
    total: int
    reserved: int
    available: int


@dataclass
class Reservation:
    reservation_id: str
    user_id: str
    request_id: str
    estimated_credits: int
    settled: bool = False


@dataclass
class PurchasedLot:
    payment_id: str
    original_credits: int
    remaining_credits: int
    created_at: datetime


class CreditLedger:
    def __init__(self, now_fn: Callable[[], datetime] | None = None) -> None:
        self._now_fn = now_fn or (lambda: datetime.now(timezone.utc))
        self._purchased: Dict[str, List[PurchasedLot]] = {}
        self._promo: Dict[str, int] = {}
        self._reservations: Dict[str, Reservation] = {}
        self._usage_events: List[dict] = []

    def mint_purchased(
        self,
        user_id: str,
        credits: int,
        payment_id: str,
        created_at: datetime | None = None,
    ) -> None:
        if credits <= 0:
            raise ValueError("credits must be positive")
        lot = PurchasedLot(
            payment_id=payment_id,
            original_credits=credits,
            remaining_credits=credits,
            created_at=created_at or self._now_fn(),
        )
        self._purchased.setdefault(user_id, []).append(lot)

    def mint_promo(self, user_id: str, credits: int, campaign_id: str) -> None:
        if credits <= 0:
            raise ValueError("credits must be positive")
        _ = campaign_id
        self._promo[user_id] = self._promo.get(user_id, 0) + credits

    def reserve(self, user_id: str, request_id: str, estimated_credits: int) -> str:
        if estimated_credits <= 0:
            raise ValueError("estimated credits must be positive")
        balance = self.balance(user_id)
        if balance.available < estimated_credits:
            raise ValueError("insufficient credits")
        reservation_id = f"res_{uuid4().hex[:12]}"
        self._reservations[reservation_id] = Reservation(
            reservation_id=reservation_id,
            user_id=user_id,
            request_id=request_id,
            estimated_credits=estimated_credits,
        )
        return reservation_id

    def settle(self, reservation_id: str, actual_credits: int) -> None:
        if actual_credits < 0:
            raise ValueError("actual credits cannot be negative")
        reservation = self._reservations.get(reservation_id)
        if reservation is None:
            raise ValueError("reservation not found")
        if reservation.settled:
            return
        self.consume(reservation.user_id, reservation.request_id, actual_credits)
        reservation.settled = True

    def consume(self, user_id: str, request_id: str, credits: int) -> None:
        if credits <= 0:
            return
        balance = self.balance(user_id)
        if balance.available < credits:
            raise ValueError("insufficient credits")

        remaining = credits
        lots = self._purchased.get(user_id, [])
        for lot in lots:
            if remaining == 0:
                break
            take = min(lot.remaining_credits, remaining)
            lot.remaining_credits -= take
            remaining -= take

        if remaining > 0:
            promo = self._promo.get(user_id, 0)
            take = min(promo, remaining)
            self._promo[user_id] = promo - take
            remaining -= take

        if remaining > 0:
            raise ValueError("insufficient credits")

        self._usage_events.append(
            {
                "request_id": request_id,
                "user_id": user_id,
                "credits": credits,
                "created_at": self._now_fn().isoformat(),
            }
        )

    def balance(self, user_id: str) -> Balance:
        purchased = sum(lot.remaining_credits for lot in self._purchased.get(user_id, []))
        promo = self._promo.get(user_id, 0)
        total = purchased + promo
        reserved = sum(
            reservation.estimated_credits
            for reservation in self._reservations.values()
            if reservation.user_id == user_id and not reservation.settled
        )
        return Balance(total=total, reserved=reserved, available=total - reserved)

    def refundable_purchased_credits(self, user_id: str, within_days: int) -> int:
        cutoff = self._now_fn() - timedelta(days=within_days)
        return sum(
            lot.remaining_credits
            for lot in self._purchased.get(user_id, [])
            if lot.created_at >= cutoff
        )

    @property
    def usage_events(self) -> List[dict]:
        return list(self._usage_events)
