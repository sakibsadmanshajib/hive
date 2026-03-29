import httpx


def test_error_response_has_openai_envelope(base_url: str):
    """Unsupported endpoint returns the exact OpenAI error envelope shape."""
    response = httpx.post(
        f"{base_url}/chat/completions",
        json={"model": "gpt-4o", "messages": [{"role": "user", "content": "hello"}]},
    )

    assert response.status_code == 404
    assert "application/json" in response.headers.get("content-type", "")

    body = response.json()
    assert "error" in body

    error = body["error"]
    assert isinstance(error["message"], str)
    assert isinstance(error["type"], str)
    assert error["param"] is None
    assert isinstance(error["code"], str)


def test_unknown_endpoint_returns_invalid_request_error(base_url: str):
    """Unknown endpoints return invalid_request_error type."""
    response = httpx.get(f"{base_url}/nonexistent")

    assert response.status_code == 404

    body = response.json()
    assert body["error"]["type"] == "invalid_request_error"
    assert body["error"]["code"] == "unknown_endpoint"
