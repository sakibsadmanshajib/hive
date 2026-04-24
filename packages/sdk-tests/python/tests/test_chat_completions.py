import os

import pytest
from openai import OpenAI

BASE_URL = os.getenv("HIVE_BASE_URL", "http://localhost:8080/v1")
API_KEY = os.getenv("HIVE_API_KEY", "test-key")
MODEL = os.getenv("HIVE_TEST_MODEL", "hive-default")


@pytest.fixture
def client() -> OpenAI:
    return OpenAI(base_url=BASE_URL, api_key=API_KEY)


def test_chat_completion_basic(client: OpenAI) -> None:
    """Official OpenAI Python SDK can call chat.completions.create and receive a valid response."""
    response = client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": "Say hello"}],
        max_tokens=256,
    )

    assert response.object == "chat.completion"
    assert len(response.choices) >= 1
    assert response.choices[0].message.role == "assistant"
    assert response.choices[0].message.content
    assert response.usage is not None
    assert response.usage.prompt_tokens > 0
    assert response.usage.completion_tokens > 0


def test_chat_completion_model_shows_alias(client: OpenAI) -> None:
    """Model field in chat completion response shows Hive alias, not provider route handle."""
    response = client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": "Hi"}],
        max_tokens=256,
    )

    assert "route-" not in response.model
    assert "openrouter" not in response.model.lower()
    assert "groq" not in response.model.lower()


def test_chat_completion_streaming(client: OpenAI) -> None:
    """Official Python SDK can stream chat completions and receive valid chunk structure."""
    chunks = []
    with client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": "Count to 3"}],
        stream=True,
        max_tokens=256,
    ) as stream:
        for chunk in stream:
            chunks.append(chunk)

    assert len(chunks) >= 1

    for chunk in chunks:
        assert chunk.object == "chat.completion.chunk"

    # At least one chunk should have non-null delta content.
    has_content = any(
        chunk.choices
        and chunk.choices[0].delta.content is not None
        and len(chunk.choices[0].delta.content) > 0
        for chunk in chunks
    )
    assert has_content
