from openai import OpenAI


def test_list_models_returns_valid_list(client: OpenAI):
    """Official OpenAI Python SDK can list models and receive a valid response."""
    response = client.models.list()

    assert response.object == "list"
    assert isinstance(response.data, list)
