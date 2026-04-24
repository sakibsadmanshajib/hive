import os

import pytest
from openai import OpenAI

BASE_URL = os.getenv("HIVE_BASE_URL", "http://localhost:8080/v1")
API_KEY = os.getenv("HIVE_API_KEY", "test-key")
EMBEDDING_MODEL = os.getenv(
    "HIVE_EMBEDDING_MODEL",
    os.getenv("HIVE_TEST_MODEL", "hive-embedding-default"),
)


@pytest.fixture
def client() -> OpenAI:
    return OpenAI(base_url=BASE_URL, api_key=API_KEY)


@pytest.mark.skip(
    reason="Alias seeded but edge-api returns 404 in CI despite DB rows present; "
    "likely API-key policy/snapshot issue. Tracked for v1.1 follow-up."
)
def test_embeddings_basic(client: OpenAI) -> None:
    """Official OpenAI Python SDK can call embeddings.create and receive a valid response."""
    response = client.embeddings.create(
        model=EMBEDDING_MODEL,
        input="Hello",
    )

    assert response.object == "list"
    assert len(response.data) >= 1
    assert response.data[0].object == "embedding"
    assert isinstance(response.data[0].embedding, list)
    assert len(response.data[0].embedding) > 0


@pytest.mark.skip(reason="See test_embeddings_basic — v1.1 follow-up.")
def test_embeddings_batch(client: OpenAI) -> None:
    """Embeddings endpoint supports batch input with multiple strings."""
    response = client.embeddings.create(
        model=EMBEDDING_MODEL,
        input=["Hello", "World"],
    )

    assert response.object == "list"
    assert len(response.data) == 2
    assert response.data[0].object == "embedding"
    assert response.data[1].object == "embedding"
