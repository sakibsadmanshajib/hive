from __future__ import annotations

import json
from dataclasses import asdict
from datetime import datetime, timezone
from typing import Dict, Tuple
from uuid import uuid4

from app.ledger import CreditLedger
from app.auth import ApiKeyService
from app.payments import PaymentService
from app.ratelimit import InMemoryRateLimiter
from app.refunds import RefundPolicy
from app.signatures import verify_bkash_signature, verify_sslcommerz_signature
from app.storage import SQLiteStore
from app.routing import ModelProfile, RoutingEngine


class GatewayApp:
    def __init__(self) -> None:
        self.ledger = CreditLedger()
        self.store = SQLiteStore(":memory:")
        self.refunds = RefundPolicy(self.ledger)
        self.models = [
            ModelProfile("fast-chat", {"chat", "code"}, 10, 12, 0.65, 420),
            ModelProfile("smart-reasoning", {"chat", "reasoning", "code"}, 18, 22, 0.9, 750),
            ModelProfile("image-basic", {"image"}, 120, 120, 0.75, 1300),
        ]
        self.routing = RoutingEngine(self.models)
        self.payments = PaymentService(self.ledger, store=self.store)
        self.auth = ApiKeyService()
        self.demo_key = self.auth.issue_key("user-1", scopes=["chat", "image", "usage", "billing"])
        self.rate_limit = InMemoryRateLimiter(limit=60, window_seconds=60)
        self.bkash_webhook_secret = "bkash-secret"
        self.sslcommerz_webhook_secret = "sslcommerz-secret"

    def handle(
        self,
        method: str,
        path: str,
        headers: Dict[str, str],
        body: str | None,
    ) -> Tuple[int, Dict[str, str], str]:
        if method == "GET" and path == "/health":
            return self._json(200, {"status": "ok", "time": datetime.now(timezone.utc).isoformat()})

        if method == "GET" and path == "/v1/models":
            data = [{"id": model.model_id, "object": "model"} for model in self.routing.list_models()]
            return self._json(200, {"object": "list", "data": data})

        if method == "GET" and path == "/v1/credits/balance":
            user_id = self._user_from_headers(headers, required_scope="billing")
            if not user_id:
                return self._json(401, {"error": "missing x-api-key"})
            if not self.rate_limit.allow(user_id):
                return self._json(429, {"error": "rate limit exceeded"})
            balance = self.ledger.balance(user_id)
            refund_quote = self.refunds.quote(user_id)
            return self._json(
                200,
                {
                    "user_id": user_id,
                    "credits": asdict(balance),
                    "refund": asdict(refund_quote),
                },
            )

        if method == "GET" and path == "/v1/usage":
            user_id = self._user_from_headers(headers, required_scope="usage")
            if not user_id:
                return self._json(401, {"error": "missing x-api-key"})
            usage = [entry for entry in self.ledger.usage_events if entry["user_id"] == user_id]
            return self._json(200, {"object": "list", "data": usage})

        if method == "POST" and path == "/v1/chat/completions":
            user_id = self._user_from_headers(headers, required_scope="chat")
            if not user_id:
                return self._json(401, {"error": "missing x-api-key"})

            payload = json.loads(body or "{}")
            task_type = payload.get("task_type", "chat")
            requested_model = payload.get("model", "auto")
            messages = payload.get("messages", [])
            combined = " ".join(str(message.get("content", "")) for message in messages)

            model = self._pick_model(requested_model, task_type)
            estimated = self._estimate_credits(model, combined)
            try:
                reservation_id = self.ledger.reserve(user_id, request_id=f"req_{uuid4().hex[:8]}", estimated_credits=estimated)
            except ValueError:
                return self._json(402, {"error": "insufficient credits"})
            actual = max(1, int(estimated * 0.8))
            self.ledger.settle(reservation_id, actual_credits=actual)
            self.store.record_usage_event(
                request_id=f"chat_{uuid4().hex[:12]}",
                user_id=user_id,
                credits=actual,
                model=model.model_id,
            )

            response_headers = {
                "x-model-routed": model.model_id,
                "x-routing-policy-version": "v1-cost-aware",
                "x-estimated-credits": str(estimated),
                "x-actual-credits": str(actual),
            }
            return self._json(
                200,
                {
                    "id": f"chatcmpl_{uuid4().hex[:12]}",
                    "object": "chat.completion",
                    "created": int(datetime.now(timezone.utc).timestamp()),
                    "model": model.model_id,
                    "choices": [
                        {
                            "index": 0,
                            "finish_reason": "stop",
                            "message": {
                                "role": "assistant",
                                "content": "MVP response: your request was processed with automatic routing.",
                            },
                        }
                    ],
                    "usage": {
                        "prompt_tokens": max(1, len(combined) // 4),
                        "completion_tokens": max(1, len(combined) // 6),
                        "total_tokens": max(2, (len(combined) // 4) + (len(combined) // 6)),
                    },
                },
                extra_headers=response_headers,
            )

        if method == "POST" and path == "/v1/responses":
            user_id = self._user_from_headers(headers, required_scope="chat")
            if not user_id:
                return self._json(401, {"error": "missing x-api-key"})
            payload = json.loads(body or "{}")
            text = str(payload.get("input", ""))
            model = self.routing.pick_auto(task_type="chat", min_quality=0.5, max_latency_ms=1000)
            estimated = self._estimate_credits(model, text)
            try:
                reservation_id = self.ledger.reserve(user_id, request_id=f"req_{uuid4().hex[:8]}", estimated_credits=estimated)
            except ValueError:
                return self._json(402, {"error": "insufficient credits"})
            actual = max(1, int(estimated * 0.9))
            self.ledger.settle(reservation_id, actual_credits=actual)
            self.store.record_usage_event(
                request_id=f"resp_{uuid4().hex[:12]}",
                user_id=user_id,
                credits=actual,
                model=model.model_id,
            )
            return self._json(
                200,
                {
                    "id": f"resp_{uuid4().hex[:12]}",
                    "object": "response",
                    "model": model.model_id,
                    "output": [{"type": "text", "text": "MVP response endpoint output."}],
                },
            )

        if method == "POST" and path == "/v1/images/generations":
            user_id = self._user_from_headers(headers, required_scope="image")
            if not user_id:
                return self._json(401, {"error": "missing x-api-key"})
            payload = json.loads(body or "{}")
            prompt = str(payload.get("prompt", ""))
            image_model = next(model for model in self.models if model.model_id == "image-basic")
            estimated = max(20, self._estimate_credits(image_model, prompt) * 8)
            try:
                reservation_id = self.ledger.reserve(user_id, request_id=f"req_{uuid4().hex[:8]}", estimated_credits=estimated)
            except ValueError:
                return self._json(402, {"error": "insufficient credits"})
            actual = max(15, int(estimated * 0.85))
            self.ledger.settle(reservation_id, actual_credits=actual)
            self.store.record_usage_event(
                request_id=f"img_{uuid4().hex[:12]}",
                user_id=user_id,
                credits=actual,
                model=image_model.model_id,
            )
            return self._json(
                200,
                {
                    "created": int(datetime.now(timezone.utc).timestamp()),
                    "object": "list",
                    "data": [{"url": "https://example.invalid/generated-image.png"}],
                },
                extra_headers={"x-actual-credits": str(actual)},
            )

        if method == "POST" and path == "/v1/payments/intents":
            payload = json.loads(body or "{}")
            user_id = payload.get("user_id", "user-1")
            amount = float(payload.get("bdt_amount", 0))
            provider = payload.get("provider", "bkash")
            intent_id = f"intent_{uuid4().hex[:10]}"
            self.payments.create_intent(intent_id, user_id=user_id, bdt_amount=amount)
            return self._json(
                201,
                {
                    "intent_id": intent_id,
                    "provider": provider,
                    "status": "initiated",
                    "redirect_url": f"https://sandbox.pay/{provider}/{intent_id}",
                },
            )

        if method == "POST" and path == "/v1/payments/webhook":
            payload = json.loads(body or "{}")
            provider = payload.get("provider", "bkash")
            if provider == "bkash":
                if not verify_bkash_signature(headers, body or "", self.bkash_webhook_secret):
                    return self._json(401, {"error": "invalid signature"})
            elif provider == "sslcommerz":
                expected_hash = payload.get("verify_sign", "")
                signed_payload = {k: str(v) for k, v in payload.items() if k != "verify_sign"}
                if not verify_sslcommerz_signature(signed_payload, expected_hash):
                    return self._json(401, {"error": "invalid signature"})

            self.payments.handle_verified_event(
                provider=provider,
                provider_txn_id=payload.get("provider_txn_id", "unknown"),
                intent_id=payload.get("intent_id", ""),
                verified=bool(payload.get("verified", False)),
            )
            return self._json(200, {"status": "accepted"})

        return self._json(404, {"error": "not found"})

    def _pick_model(self, requested_model: str, task_type: str) -> ModelProfile:
        if requested_model != "auto":
            for model in self.models:
                if model.model_id == requested_model:
                    return model
            raise ValueError("unknown model")
        return self.routing.pick_auto(task_type=task_type, min_quality=0.5, max_latency_ms=1000)

    @staticmethod
    def _estimate_credits(model: ModelProfile, text: str) -> int:
        prompt_tokens = max(1, len(text) // 4)
        output_tokens = max(1, int(prompt_tokens * 1.2))
        input_cost = (prompt_tokens / 1000.0) * model.credits_per_1k_input
        output_cost = (output_tokens / 1000.0) * model.credits_per_1k_output
        return max(1, int(input_cost + output_cost + 1))

    def _user_from_headers(self, headers: Dict[str, str], required_scope: str) -> str | None:
        key = headers.get("x-api-key") or headers.get("X-API-Key")
        if not key:
            return None
        if key.startswith("dev-user-"):
            return f"user-{key.split('dev-user-')[-1]}"
        return self.auth.validate_key(key, required_scope=required_scope)

    @staticmethod
    def _json(status: int, payload: dict, extra_headers: Dict[str, str] | None = None) -> Tuple[int, Dict[str, str], str]:
        headers = {"content-type": "application/json"}
        if extra_headers:
            headers.update(extra_headers)
        return status, headers, json.dumps(payload)
