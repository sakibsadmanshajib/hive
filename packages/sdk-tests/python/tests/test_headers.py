import httpx


def test_compat_headers_on_success_response(base_url: str):
    """Compatibility headers appear on successful responses."""
    response = httpx.get(f"{base_url}/models")

    assert response.status_code == 200

    request_id = response.headers.get("x-request-id")
    assert request_id is not None
    assert len(request_id) > 0

    assert response.headers.get("openai-version") == "2020-10-01"

    processing_ms = response.headers.get("openai-processing-ms")
    assert processing_ms is not None
    assert int(processing_ms) >= 0


def test_compat_headers_on_error_response(base_url: str):
    """Compatibility headers appear on error responses too."""
    response = httpx.post(
        f"{base_url}/chat/completions",
        json={},
    )

    assert response.status_code == 404

    request_id = response.headers.get("x-request-id")
    assert request_id is not None
    assert len(request_id) > 0

    assert response.headers.get("openai-version") == "2020-10-01"

    processing_ms = response.headers.get("openai-processing-ms")
    assert processing_ms is not None
    assert int(processing_ms) >= 0


def test_unique_request_ids(base_url: str):
    """Each request gets a unique x-request-id."""
    r1 = httpx.get(f"{base_url}/models")
    r2 = httpx.get(f"{base_url}/models")

    id1 = r1.headers.get("x-request-id")
    id2 = r2.headers.get("x-request-id")

    assert id1 is not None
    assert id2 is not None
    assert id1 != id2
