# Provider Fallback Matrix

| Task Type | Primary Model | Fallback 1 | Fallback 2 | Trigger |
| --- | --- | --- | --- | --- |
| chat | fast-chat | smart-reasoning | fast-chat | timeout > 4s or 5xx |
| code | fast-chat | smart-reasoning | fast-chat | quality or tool failure |
| reasoning | smart-reasoning | fast-chat | smart-reasoning | cost spike or timeout |
| image | image-basic | image-basic | image-basic | provider unavailable |
