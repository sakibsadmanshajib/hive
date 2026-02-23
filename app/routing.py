from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable, Set


@dataclass(frozen=True)
class ModelProfile:
    model_id: str
    capabilities: Set[str]
    credits_per_1k_input: int
    credits_per_1k_output: int
    quality: float
    latency_ms: int

    @property
    def blended_cost(self) -> float:
        return (self.credits_per_1k_input + self.credits_per_1k_output) / 2.0


class RoutingEngine:
    def __init__(self, models: Iterable[ModelProfile]) -> None:
        self.models = list(models)

    def pick_auto(self, task_type: str, min_quality: float, max_latency_ms: int) -> ModelProfile:
        eligible = [
            model
            for model in self.models
            if task_type in model.capabilities and model.quality >= min_quality and model.latency_ms <= max_latency_ms
        ]
        if not eligible:
            raise ValueError("no eligible model")
        return sorted(eligible, key=lambda m: (m.blended_cost, -m.quality))[0]

    def list_models(self) -> list[ModelProfile]:
        return list(self.models)
