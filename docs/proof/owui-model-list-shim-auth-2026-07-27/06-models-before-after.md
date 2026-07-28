# GET /v1/models with the Open WebUI shim key, before and after

Both containers run the same image env, the same control-plane, and the same
OWUI_SHIM_KEY. The only difference is the edge-api source: `hive-edge-api:beforefix`
is built from origin/main (5ad32f62) and the fixed container is built from this
branch. The key is masked below as `hk_...voI4`.

## Before, pre-fix edge-api

```
$ curl -s -w "\nHTTP %{http_code}\n" http://localhost:18086/v1/models \
    -H "Authorization: Bearer hk_...voI4"
{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}

HTTP 401
```

## After, this branch

```
$ curl -s -w "\nHTTP %{http_code}\n" http://localhost:8080/v1/models \
    -H "Authorization: Bearer hk_...voI4"
{
    "data": [
        {
            "id": "hive-auto",
            "object": "model",
            "created": 1774961894,
            "owned_by": "hive"
        },
        {
            "id": "hive-default",
            "object": "model",
            "created": 1774961894,
            "owned_by": "hive"
        },
        {
            "id": "hive-embedding-default",
            "object": "model",
            "created": 1777002349,
            "owned_by": "hive"
        },
        {
            "id": "hive-fast",
            "object": "model",
            "created": 1774961894,
            "owned_by": "hive"
        },
        {
            "id": "hive-stt",
            "object": "model",
            "created": 1784317834,
            "owned_by": "hive"
        },
        {
            "id": "hive-tts",
            "object": "model",
            "created": 1784317834,
            "owned_by": "hive"
        }
    ],
    "object": "list"
}
HTTP 200
```

## The same list is already public with no authentication at all

```
$ curl -s -o /dev/null -w "HTTP %{http_code}\n" http://localhost:8080/catalog/models
HTTP 200
```
