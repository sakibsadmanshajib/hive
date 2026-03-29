import os

import pytest
from openai import OpenAI


@pytest.fixture()
def client():
    base_url = os.environ.get("HIVE_BASE_URL", "http://localhost:8080/v1")
    return OpenAI(base_url=base_url, api_key="test-key")


@pytest.fixture()
def base_url():
    return os.environ.get("HIVE_BASE_URL", "http://localhost:8080/v1")
