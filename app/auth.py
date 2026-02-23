from __future__ import annotations

import secrets
from dataclasses import dataclass


@dataclass
class _ApiKeyRecord:
    user_id: str
    scopes: set[str]
    revoked: bool = False


class ApiKeyService:
    def __init__(self) -> None:
        self._store: dict[str, _ApiKeyRecord] = {}

    def issue_key(self, user_id: str, scopes: list[str]) -> str:
        key = secrets.token_urlsafe(32)
        self._store[key] = _ApiKeyRecord(user_id=user_id, scopes=set(scopes))
        return key

    def validate_key(self, key: str, required_scope: str) -> str | None:
        record = self._store.get(key)
        if record is None:
            return None
        if record.revoked:
            return None
        if required_scope not in record.scopes:
            return None
        return record.user_id

    def revoke_key(self, key: str) -> None:
        record = self._store.get(key)
        if record is None:
            return
        record.revoked = True
