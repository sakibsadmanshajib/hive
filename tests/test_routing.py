import unittest

from app.routing import ModelProfile, RoutingEngine


class RoutingTests(unittest.TestCase):
    def test_auto_selects_lowest_cost_acceptable_model(self) -> None:
        models = [
            ModelProfile(model_id="fast-chat", capabilities={"chat"}, credits_per_1k_input=10, credits_per_1k_output=12, quality=0.6, latency_ms=450),
            ModelProfile(model_id="smart-chat", capabilities={"chat", "reasoning"}, credits_per_1k_input=20, credits_per_1k_output=24, quality=0.9, latency_ms=700),
            ModelProfile(model_id="cheap-low", capabilities={"chat"}, credits_per_1k_input=5, credits_per_1k_output=5, quality=0.2, latency_ms=300),
        ]
        engine = RoutingEngine(models)

        routed = engine.pick_auto(task_type="chat", min_quality=0.5, max_latency_ms=1000)
        self.assertEqual(routed.model_id, "fast-chat")


if __name__ == "__main__":
    unittest.main()
