import httpx


def test_health_returns_200_with_status_ok(base_url: str):
    """Health endpoint returns 200 with {"status": "ok"}."""
    # Strip /v1 suffix to reach the root health endpoint
    root_url = base_url.rstrip("/").removesuffix("/v1")
    response = httpx.get(f"{root_url}/health")

    assert response.status_code == 200
    body = response.json()
    assert body == {"status": "ok"}
