# Hive API Support Matrix

This document lists every public endpoint from the OpenAI API and its current support status in Hive.

**Statuses:**
- **Supported Now** -- Fully implemented and available
- **Planned for Launch** -- Will be implemented before launch
- **Explicitly Unsupported at Launch** -- Not planned for launch; returns structured error
- **Out of Scope** -- Organization/admin endpoints not part of Hive

## Supported Now (1 endpoints)

| Method | Path | Phase | Notes |
|--------|------|-------|-------|
| GET | `/v1/models` | 1 | Lists available models |

## Planned for Launch (24 endpoints)

| Method | Path | Phase | Notes |
|--------|------|-------|-------|
| POST | `/v1/audio/speech` | 6 | Text-to-speech |
| POST | `/v1/audio/transcriptions` | 6 | Speech-to-text |
| POST | `/v1/audio/translations` | 6 | Audio translation to English |
| GET | `/v1/batches` | 7 | List batches |
| POST | `/v1/batches` | 7 | Create batch |
| GET | `/v1/batches/{batch_id}` | 7 | Retrieve batch |
| POST | `/v1/batches/{batch_id}/cancel` | 7 | Cancel batch |
| POST | `/v1/chat/completions` | 6 | Chat completion inference |
| POST | `/v1/completions` | 6 | Legacy completion inference |
| POST | `/v1/embeddings` | 6 | Text embeddings |
| GET | `/v1/files` | 5 | List files |
| POST | `/v1/files` | 5 | Upload a file |
| GET | `/v1/files/{file_id}` | 5 | Retrieve file metadata |
| DELETE | `/v1/files/{file_id}` | 5 | Delete a file |
| GET | `/v1/files/{file_id}/content` | 5 | Download file content |
| POST | `/v1/images/edits` | 6 | Image editing |
| POST | `/v1/images/generations` | 6 | Image generation |
| GET | `/v1/models/{model}` | 1 | Retrieves a model instance |
| DELETE | `/v1/models/{model}` | 1 | Delete a fine-tuned model |
| POST | `/v1/responses` | 6 | Responses API inference |
| POST | `/v1/uploads` | 5 | Create upload |
| POST | `/v1/uploads/{upload_id}/cancel` | 5 | Cancel upload |
| POST | `/v1/uploads/{upload_id}/complete` | 5 | Complete upload |
| POST | `/v1/uploads/{upload_id}/parts` | 5 | Add upload part |

## Explicitly Unsupported at Launch (72 endpoints)

| Method | Path | Phase | Notes |
|--------|------|-------|-------|
| GET | `/v1/assistants` | -- | Returns a list of assistants. |
| POST | `/v1/assistants` | -- | Create an assistant with a model and instructions. |
| GET | `/v1/assistants/{assistant_id}` | -- | Retrieves an assistant. |
| POST | `/v1/assistants/{assistant_id}` | -- | Modifies an assistant. |
| DELETE | `/v1/assistants/{assistant_id}` | -- | Delete an assistant. |
| GET | `/v1/chat/completions` | -- | List stored Chat Completions. Only Chat Completions that have been stored |
| GET | `/v1/chat/completions/{completion_id}` | -- | Get a stored chat completion. Only Chat Completions that have been created |
| POST | `/v1/chat/completions/{completion_id}` | -- | Modify a stored chat completion. Only Chat Completions that have been |
| DELETE | `/v1/chat/completions/{completion_id}` | -- | Delete a stored chat completion. Only Chat Completions that have been |
| GET | `/v1/chat/completions/{completion_id}/messages` | -- | Get the messages in a stored chat completion. Only Chat Completions that |
| GET | `/v1/evals` | -- | List evaluations for a project. |
| POST | `/v1/evals` | -- | Create the structure of an evaluation that can be used to test a model's perform |
| GET | `/v1/evals/{eval_id}` | -- | Get an evaluation by ID. |
| POST | `/v1/evals/{eval_id}` | -- | Update certain properties of an evaluation. |
| DELETE | `/v1/evals/{eval_id}` | -- | Delete an evaluation. |
| GET | `/v1/evals/{eval_id}/runs` | -- | Get a list of runs for an evaluation. |
| POST | `/v1/evals/{eval_id}/runs` | -- | Create a new evaluation run. This is the endpoint that will kick off grading. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}` | -- | Get an evaluation run by ID. |
| POST | `/v1/evals/{eval_id}/runs/{run_id}` | -- | Cancel an ongoing evaluation run. |
| DELETE | `/v1/evals/{eval_id}/runs/{run_id}` | -- | Delete an eval run. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}/output_items` | -- | Get a list of output items for an evaluation run. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}/output_items/{output_item_id}` | -- | Get an evaluation run output item by ID. |
| GET | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions` | -- | **NOTE:** This endpoint requires an [admin API key](../admin-api-keys). |
| POST | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions` | -- | **NOTE:** Calling this endpoint requires an [admin API key](../admin-api-keys). |
| DELETE | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions/{permission_id}` | -- | **NOTE:** This endpoint requires an [admin API key](../admin-api-keys). |
| GET | `/v1/fine_tuning/jobs` | -- | List your organization's fine-tuning jobs |
| POST | `/v1/fine_tuning/jobs` | -- | Creates a fine-tuning job which begins the process of creating a new model from  |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}` | -- | Get info about a fine-tuning job. |
| POST | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/cancel` | -- | Immediately cancel a fine-tune job. |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/checkpoints` | -- | List checkpoints for a fine-tuning job. |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/events` | -- | Get status updates for a fine-tuning job. |
| POST | `/v1/images/variations` | -- | Creates a variation of a given image. This endpoint only supports `dall-e-2`. |
| POST | `/v1/moderations` | -- | Classifies if text and/or image inputs are potentially harmful. Learn |
| POST | `/v1/realtime/sessions` | -- | Create an ephemeral API token for use in client-side applications with the |
| POST | `/v1/realtime/transcription_sessions` | -- | Create an ephemeral API token for use in client-side applications with the |
| GET | `/v1/responses/{response_id}` | -- | Retrieves a model response with the given ID. |
| DELETE | `/v1/responses/{response_id}` | -- | Deletes a model response with the given ID. |
| GET | `/v1/responses/{response_id}/input_items` | -- | Returns a list of input items for a given response. |
| POST | `/v1/threads` | -- | Create a thread. |
| POST | `/v1/threads/runs` | -- | Create a thread and run it in one request. |
| GET | `/v1/threads/{thread_id}` | -- | Retrieves a thread. |
| POST | `/v1/threads/{thread_id}` | -- | Modifies a thread. |
| DELETE | `/v1/threads/{thread_id}` | -- | Delete a thread. |
| GET | `/v1/threads/{thread_id}/messages` | -- | Returns a list of messages for a given thread. |
| POST | `/v1/threads/{thread_id}/messages` | -- | Create a message. |
| GET | `/v1/threads/{thread_id}/messages/{message_id}` | -- | Retrieve a message. |
| POST | `/v1/threads/{thread_id}/messages/{message_id}` | -- | Modifies a message. |
| DELETE | `/v1/threads/{thread_id}/messages/{message_id}` | -- | Deletes a message. |
| GET | `/v1/threads/{thread_id}/runs` | -- | Returns a list of runs belonging to a thread. |
| POST | `/v1/threads/{thread_id}/runs` | -- | Create a run. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}` | -- | Retrieves a run. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}` | -- | Modifies a run. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}/cancel` | -- | Cancels a run that is `in_progress`. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}/steps` | -- | Returns a list of run steps belonging to a run. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}/steps/{step_id}` | -- | Retrieves a run step. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}/submit_tool_outputs` | -- | When a run has the `status: "requires_action"` and `required_action.type` is `su |
| GET | `/v1/vector_stores` | -- | Returns a list of vector stores. |
| POST | `/v1/vector_stores` | -- | Create a vector store. |
| GET | `/v1/vector_stores/{vector_store_id}` | -- | Retrieves a vector store. |
| POST | `/v1/vector_stores/{vector_store_id}` | -- | Modifies a vector store. |
| DELETE | `/v1/vector_stores/{vector_store_id}` | -- | Delete a vector store. |
| POST | `/v1/vector_stores/{vector_store_id}/file_batches` | -- | Create a vector store file batch. |
| GET | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}` | -- | Retrieves a vector store file batch. |
| POST | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}/cancel` | -- | Cancel a vector store file batch. This attempts to cancel the processing of file |
| GET | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}/files` | -- | Returns a list of vector store files in a batch. |
| GET | `/v1/vector_stores/{vector_store_id}/files` | -- | Returns a list of vector store files. |
| POST | `/v1/vector_stores/{vector_store_id}/files` | -- | Create a vector store file by attaching a [File](/docs/api-reference/files) to a |
| GET | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | -- | Retrieves a vector store file. |
| POST | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | -- | Update attributes on a vector store file. |
| DELETE | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | -- | Delete a vector store file. This will remove the file from the vector store but  |
| GET | `/v1/vector_stores/{vector_store_id}/files/{file_id}/content` | -- | Retrieve the parsed contents of a vector store file. |
| POST | `/v1/vector_stores/{vector_store_id}/search` | -- | Search a vector store for relevant chunks based on a query and file attributes f |

## Out of Scope (51 endpoints)

| Method | Path | Phase | Notes |
|--------|------|-------|-------|
| GET | `/v1/organization/admin_api_keys` | -- | List organization API keys |
| POST | `/v1/organization/admin_api_keys` | -- | Create an organization admin API key |
| GET | `/v1/organization/admin_api_keys/{key_id}` | -- | Retrieve a single organization API key |
| DELETE | `/v1/organization/admin_api_keys/{key_id}` | -- | Delete an organization admin API key |
| GET | `/v1/organization/audit_logs` | -- | List user actions and configuration changes within this organization. |
| GET | `/v1/organization/certificates` | -- | List uploaded certificates for this organization. |
| POST | `/v1/organization/certificates` | -- | Upload a certificate to the organization. This does **not** automatically activa |
| POST | `/v1/organization/certificates/activate` | -- | Activate certificates at the organization level. |
| POST | `/v1/organization/certificates/deactivate` | -- | Deactivate certificates at the organization level. |
| GET | `/v1/organization/certificates/{certificate_id}` | -- | Get a certificate that has been uploaded to the organization. |
| POST | `/v1/organization/certificates/{certificate_id}` | -- | Modify a certificate. Note that only the name can be modified. |
| DELETE | `/v1/organization/certificates/{certificate_id}` | -- | Delete a certificate from the organization. |
| GET | `/v1/organization/costs` | -- | Get costs details for the organization. |
| GET | `/v1/organization/invites` | -- | Returns a list of invites in the organization. |
| POST | `/v1/organization/invites` | -- | Create an invite for a user to the organization. The invite must be accepted by  |
| GET | `/v1/organization/invites/{invite_id}` | -- | Retrieves an invite. |
| DELETE | `/v1/organization/invites/{invite_id}` | -- | Delete an invite. If the invite has already been accepted, it cannot be deleted. |
| GET | `/v1/organization/projects` | -- | Returns a list of projects. |
| POST | `/v1/organization/projects` | -- | Create a new project in the organization. Projects can be created and archived,  |
| GET | `/v1/organization/projects/{project_id}` | -- | Retrieves a project. |
| POST | `/v1/organization/projects/{project_id}` | -- | Modifies a project in the organization. |
| GET | `/v1/organization/projects/{project_id}/api_keys` | -- | Returns a list of API keys in the project. |
| GET | `/v1/organization/projects/{project_id}/api_keys/{key_id}` | -- | Retrieves an API key in the project. |
| DELETE | `/v1/organization/projects/{project_id}/api_keys/{key_id}` | -- | Deletes an API key from the project. |
| POST | `/v1/organization/projects/{project_id}/archive` | -- | Archives a project in the organization. Archived projects cannot be used or upda |
| GET | `/v1/organization/projects/{project_id}/certificates` | -- | List certificates for this project. |
| POST | `/v1/organization/projects/{project_id}/certificates/activate` | -- | Activate certificates at the project level. |
| POST | `/v1/organization/projects/{project_id}/certificates/deactivate` | -- | Deactivate certificates at the project level. |
| GET | `/v1/organization/projects/{project_id}/rate_limits` | -- | Returns the rate limits per model for a project. |
| POST | `/v1/organization/projects/{project_id}/rate_limits/{rate_limit_id}` | -- | Updates a project rate limit. |
| GET | `/v1/organization/projects/{project_id}/service_accounts` | -- | Returns a list of service accounts in the project. |
| POST | `/v1/organization/projects/{project_id}/service_accounts` | -- | Creates a new service account in the project. This also returns an unredacted AP |
| GET | `/v1/organization/projects/{project_id}/service_accounts/{service_account_id}` | -- | Retrieves a service account in the project. |
| DELETE | `/v1/organization/projects/{project_id}/service_accounts/{service_account_id}` | -- | Deletes a service account from the project. |
| GET | `/v1/organization/projects/{project_id}/users` | -- | Returns a list of users in the project. |
| POST | `/v1/organization/projects/{project_id}/users` | -- | Adds a user to the project. Users must already be members of the organization to |
| GET | `/v1/organization/projects/{project_id}/users/{user_id}` | -- | Retrieves a user in the project. |
| POST | `/v1/organization/projects/{project_id}/users/{user_id}` | -- | Modifies a user's role in the project. |
| DELETE | `/v1/organization/projects/{project_id}/users/{user_id}` | -- | Deletes a user from the project. |
| GET | `/v1/organization/usage/audio_speeches` | -- | Get audio speeches usage details for the organization. |
| GET | `/v1/organization/usage/audio_transcriptions` | -- | Get audio transcriptions usage details for the organization. |
| GET | `/v1/organization/usage/code_interpreter_sessions` | -- | Get code interpreter sessions usage details for the organization. |
| GET | `/v1/organization/usage/completions` | -- | Get completions usage details for the organization. |
| GET | `/v1/organization/usage/embeddings` | -- | Get embeddings usage details for the organization. |
| GET | `/v1/organization/usage/images` | -- | Get images usage details for the organization. |
| GET | `/v1/organization/usage/moderations` | -- | Get moderations usage details for the organization. |
| GET | `/v1/organization/usage/vector_stores` | -- | Get vector stores usage details for the organization. |
| GET | `/v1/organization/users` | -- | Lists all of the users in the organization. |
| GET | `/v1/organization/users/{user_id}` | -- | Retrieves a user by their identifier. |
| POST | `/v1/organization/users/{user_id}` | -- | Modifies a user's role in the organization. |
| DELETE | `/v1/organization/users/{user_id}` | -- | Deletes a user from the organization. |
