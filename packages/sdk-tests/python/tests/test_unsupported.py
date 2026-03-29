import openai
import pytest
from openai import OpenAI


def test_chat_completions_raises_not_found_with_planned_status(client: OpenAI):
    """Chat completions (planned) raises NotFoundError with unsupported_endpoint type."""
    with pytest.raises(openai.NotFoundError) as exc_info:
        client.chat.completions.create(
            model="gpt-4o",
            messages=[{"role": "user", "content": "hello"}],
        )

    err = exc_info.value
    assert err.status_code == 404

    body = err.body
    assert isinstance(body, dict)
    error_obj = body.get("error", body)
    assert error_obj["type"] == "unsupported_endpoint"
    assert error_obj["code"] == "endpoint_not_available"

    message = error_obj["message"]
    assert "planned but not yet available" in message

    # Provider-blind: no mention of provider, upstream, or OpenAI
    message_lower = message.lower()
    assert "provider" not in message_lower
    assert "upstream" not in message_lower
    assert "openai" not in message_lower


def test_fine_tuning_raises_not_found_with_unsupported_status(client: OpenAI):
    """Fine-tuning (explicitly unsupported) raises NotFoundError with endpoint_unsupported code."""
    with pytest.raises(openai.NotFoundError) as exc_info:
        client.fine_tuning.jobs.create(
            model="gpt-4o",
            training_file="file-abc123",
        )

    err = exc_info.value
    assert err.status_code == 404

    body = err.body
    assert isinstance(body, dict)
    error_obj = body.get("error", body)
    assert error_obj["type"] == "unsupported_endpoint"
    assert error_obj["code"] == "endpoint_unsupported"
