import os

import pytest
from openai import OpenAI


def _base_url() -> str:
    return os.environ.get("HIVE_BASE_URL", "http://localhost:8080/v1")


def _api_key() -> str:
    return os.environ.get("HIVE_API_KEY") or "test-key"


@pytest.fixture()
def client():
    return OpenAI(base_url=_base_url(), api_key=_api_key())


@pytest.fixture()
def base_url():
    return _base_url()
