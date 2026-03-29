# Hive API Support Matrix

Generated from `packages/openai-contract/matrix/support-matrix.json`. Do not edit manually.

## supported_now (1 endpoints)

| Method | Path | Status | Phase | Notes |
|--------|------|--------|-------|-------|
| GET | `/v1/models` | supported_now | 1 | Lists available models |


## planned_for_launch (24 endpoints)

| Method | Path | Status | Phase | Notes |
|--------|------|--------|-------|-------|
| POST | `/v1/audio/speech` | planned_for_launch | 6 | Text-to-speech |
| POST | `/v1/audio/transcriptions` | planned_for_launch | 6 | Speech-to-text |
| POST | `/v1/audio/translations` | planned_for_launch | 6 | Audio translation to English |
| GET | `/v1/batches` | planned_for_launch | 7 | List batches |
| POST | `/v1/batches` | planned_for_launch | 7 | Create batch |
| GET | `/v1/batches/{batch_id}` | planned_for_launch | 7 | Retrieve batch |
| POST | `/v1/batches/{batch_id}/cancel` | planned_for_launch | 7 | Cancel batch |
| POST | `/v1/chat/completions` | planned_for_launch | 6 | Chat completion inference |
| POST | `/v1/completions` | planned_for_launch | 6 | Legacy completion inference |
| POST | `/v1/embeddings` | planned_for_launch | 6 | Text embeddings |
| GET | `/v1/files` | planned_for_launch | 5 | List files |
| POST | `/v1/files` | planned_for_launch | 5 | Upload a file |
| GET | `/v1/files/{file_id}` | planned_for_launch | 5 | Retrieve file metadata |
| DELETE | `/v1/files/{file_id}` | planned_for_launch | 5 | Delete a file |
| GET | `/v1/files/{file_id}/content` | planned_for_launch | 5 | Download file content |
| POST | `/v1/images/edits` | planned_for_launch | 6 | Image editing |
| POST | `/v1/images/generations` | planned_for_launch | 6 | Image generation |
| GET | `/v1/models/{model}` | planned_for_launch | 1 | Retrieves a model instance |
| DELETE | `/v1/models/{model}` | planned_for_launch | 1 | Delete a fine-tuned model |
| POST | `/v1/responses` | planned_for_launch | 6 | Responses API inference |
| POST | `/v1/uploads` | planned_for_launch | 5 | Create upload |
| POST | `/v1/uploads/{upload_id}/cancel` | planned_for_launch | 5 | Cancel upload |
| POST | `/v1/uploads/{upload_id}/complete` | planned_for_launch | 5 | Complete upload |
| POST | `/v1/uploads/{upload_id}/parts` | planned_for_launch | 5 | Add upload part |


## explicitly_unsupported_at_launch (72 endpoints)

| Method | Path | Status | Phase | Notes |
|--------|------|--------|-------|-------|
| GET | `/v1/assistants` | explicitly_unsupported_at_launch | -- | Returns a list of assistants. |
| POST | `/v1/assistants` | explicitly_unsupported_at_launch | -- | Create an assistant with a model and instructions. |
| GET | `/v1/assistants/{assistant_id}` | explicitly_unsupported_at_launch | -- | Retrieves an assistant. |
| POST | `/v1/assistants/{assistant_id}` | explicitly_unsupported_at_launch | -- | Modifies an assistant. |
| DELETE | `/v1/assistants/{assistant_id}` | explicitly_unsupported_at_launch | -- | Delete an assistant. |
| GET | `/v1/chat/completions` | explicitly_unsupported_at_launch | -- | List stored Chat Completions. Only Chat Completions that have been stored |
| GET | `/v1/chat/completions/{completion_id}` | explicitly_unsupported_at_launch | -- | Get a stored chat completion. Only Chat Completions that have been created |
| POST | `/v1/chat/completions/{completion_id}` | explicitly_unsupported_at_launch | -- | Modify a stored chat completion. Only Chat Completions that have been |
| DELETE | `/v1/chat/completions/{completion_id}` | explicitly_unsupported_at_launch | -- | Delete a stored chat completion. Only Chat Completions that have been |
| GET | `/v1/chat/completions/{completion_id}/messages` | explicitly_unsupported_at_launch | -- | Get the messages in a stored chat completion. Only Chat Completions that |
| GET | `/v1/evals` | explicitly_unsupported_at_launch | -- | List evaluations for a project. |
| POST | `/v1/evals` | explicitly_unsupported_at_launch | -- | Create the structure of an evaluation that can be used to test a model's perform |
| GET | `/v1/evals/{eval_id}` | explicitly_unsupported_at_launch | -- | Get an evaluation by ID. |
| POST | `/v1/evals/{eval_id}` | explicitly_unsupported_at_launch | -- | Update certain properties of an evaluation. |
| DELETE | `/v1/evals/{eval_id}` | explicitly_unsupported_at_launch | -- | Delete an evaluation. |
| GET | `/v1/evals/{eval_id}/runs` | explicitly_unsupported_at_launch | -- | Get a list of runs for an evaluation. |
| POST | `/v1/evals/{eval_id}/runs` | explicitly_unsupported_at_launch | -- | Create a new evaluation run. This is the endpoint that will kick off grading. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}` | explicitly_unsupported_at_launch | -- | Get an evaluation run by ID. |
| POST | `/v1/evals/{eval_id}/runs/{run_id}` | explicitly_unsupported_at_launch | -- | Cancel an ongoing evaluation run. |
| DELETE | `/v1/evals/{eval_id}/runs/{run_id}` | explicitly_unsupported_at_launch | -- | Delete an eval run. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}/output_items` | explicitly_unsupported_at_launch | -- | Get a list of output items for an evaluation run. |
| GET | `/v1/evals/{eval_id}/runs/{run_id}/output_items/{output_item_id}` | explicitly_unsupported_at_launch | -- | Get an evaluation run output item by ID. |
| GET | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions` | explicitly_unsupported_at_launch | -- | **NOTE:** This endpoint requires an [admin API key](../admin-api-keys). |
| POST | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions` | explicitly_unsupported_at_launch | -- | **NOTE:** Calling this endpoint requires an [admin API key](../admin-api-keys). |
| DELETE | `/v1/fine_tuning/checkpoints/{fine_tuned_model_checkpoint}/permissions/{permission_id}` | explicitly_unsupported_at_launch | -- | **NOTE:** This endpoint requires an [admin API key](../admin-api-keys). |
| GET | `/v1/fine_tuning/jobs` | explicitly_unsupported_at_launch | -- | List your organization's fine-tuning jobs |
| POST | `/v1/fine_tuning/jobs` | explicitly_unsupported_at_launch | -- | Creates a fine-tuning job which begins the process of creating a new model from |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}` | explicitly_unsupported_at_launch | -- | Get info about a fine-tuning job. |
| POST | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/cancel` | explicitly_unsupported_at_launch | -- | Immediately cancel a fine-tune job. |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/checkpoints` | explicitly_unsupported_at_launch | -- | List checkpoints for a fine-tuning job. |
| GET | `/v1/fine_tuning/jobs/{fine_tuning_job_id}/events` | explicitly_unsupported_at_launch | -- | Get status updates for a fine-tuning job. |
| POST | `/v1/images/variations` | explicitly_unsupported_at_launch | -- | Creates a variation of a given image. This endpoint only supports `dall-e-2`. |
| POST | `/v1/moderations` | explicitly_unsupported_at_launch | -- | Classifies if text and/or image inputs are potentially harmful. Learn |
| POST | `/v1/realtime/sessions` | explicitly_unsupported_at_launch | -- | Create an ephemeral API token for use in client-side applications with the |
| POST | `/v1/realtime/transcription_sessions` | explicitly_unsupported_at_launch | -- | Create an ephemeral API token for use in client-side applications with the |
| GET | `/v1/responses/{response_id}` | explicitly_unsupported_at_launch | -- | Retrieves a model response with the given ID. |
| DELETE | `/v1/responses/{response_id}` | explicitly_unsupported_at_launch | -- | Deletes a model response with the given ID. |
| GET | `/v1/responses/{response_id}/input_items` | explicitly_unsupported_at_launch | -- | Returns a list of input items for a given response. |
| POST | `/v1/threads` | explicitly_unsupported_at_launch | -- | Create a thread. |
| POST | `/v1/threads/runs` | explicitly_unsupported_at_launch | -- | Create a thread and run it in one request. |
| GET | `/v1/threads/{thread_id}` | explicitly_unsupported_at_launch | -- | Retrieves a thread. |
| POST | `/v1/threads/{thread_id}` | explicitly_unsupported_at_launch | -- | Modifies a thread. |
| DELETE | `/v1/threads/{thread_id}` | explicitly_unsupported_at_launch | -- | Delete a thread. |
| GET | `/v1/threads/{thread_id}/messages` | explicitly_unsupported_at_launch | -- | Returns a list of messages for a given thread. |
| POST | `/v1/threads/{thread_id}/messages` | explicitly_unsupported_at_launch | -- | Create a message. |
| GET | `/v1/threads/{thread_id}/messages/{message_id}` | explicitly_unsupported_at_launch | -- | Retrieve a message. |
| POST | `/v1/threads/{thread_id}/messages/{message_id}` | explicitly_unsupported_at_launch | -- | Modifies a message. |
| DELETE | `/v1/threads/{thread_id}/messages/{message_id}` | explicitly_unsupported_at_launch | -- | Deletes a message. |
| GET | `/v1/threads/{thread_id}/runs` | explicitly_unsupported_at_launch | -- | Returns a list of runs belonging to a thread. |
| POST | `/v1/threads/{thread_id}/runs` | explicitly_unsupported_at_launch | -- | Create a run. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}` | explicitly_unsupported_at_launch | -- | Retrieves a run. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}` | explicitly_unsupported_at_launch | -- | Modifies a run. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}/cancel` | explicitly_unsupported_at_launch | -- | Cancels a run that is `in_progress`. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}/steps` | explicitly_unsupported_at_launch | -- | Returns a list of run steps belonging to a run. |
| GET | `/v1/threads/{thread_id}/runs/{run_id}/steps/{step_id}` | explicitly_unsupported_at_launch | -- | Retrieves a run step. |
| POST | `/v1/threads/{thread_id}/runs/{run_id}/submit_tool_outputs` | explicitly_unsupported_at_launch | -- | When a run has the `status: "requires_action"` and `required_action.type` is `su |
| GET | `/v1/vector_stores` | explicitly_unsupported_at_launch | -- | Returns a list of vector stores. |
| POST | `/v1/vector_stores` | explicitly_unsupported_at_launch | -- | Create a vector store. |
| GET | `/v1/vector_stores/{vector_store_id}` | explicitly_unsupported_at_launch | -- | Retrieves a vector store. |
| POST | `/v1/vector_stores/{vector_store_id}` | explicitly_unsupported_at_launch | -- | Modifies a vector store. |
| DELETE | `/v1/vector_stores/{vector_store_id}` | explicitly_unsupported_at_launch | -- | Delete a vector store. |
| POST | `/v1/vector_stores/{vector_store_id}/file_batches` | explicitly_unsupported_at_launch | -- | Create a vector store file batch. |
| GET | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}` | explicitly_unsupported_at_launch | -- | Retrieves a vector store file batch. |
| POST | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}/cancel` | explicitly_unsupported_at_launch | -- | Cancel a vector store file batch. This attempts to cancel the processing of file |
| GET | `/v1/vector_stores/{vector_store_id}/file_batches/{batch_id}/files` | explicitly_unsupported_at_launch | -- | Returns a list of vector store files in a batch. |
| GET | `/v1/vector_stores/{vector_store_id}/files` | explicitly_unsupported_at_launch | -- | Returns a list of vector store files. |
| POST | `/v1/vector_stores/{vector_store_id}/files` | explicitly_unsupported_at_launch | -- | Create a vector store file by attaching a [File](/docs/api-reference/files) to a |
| GET | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | explicitly_unsupported_at_launch | -- | Retrieves a vector store file. |
| POST | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | explicitly_unsupported_at_launch | -- | Update attributes on a vector store file. |
| DELETE | `/v1/vector_stores/{vector_store_id}/files/{file_id}` | explicitly_unsupported_at_launch | -- | Delete a vector store file. This will remove the file from the vector store but |
| GET | `/v1/vector_stores/{vector_store_id}/files/{file_id}/content` | explicitly_unsupported_at_launch | -- | Retrieve the parsed contents of a vector store file. |
| POST | `/v1/vector_stores/{vector_store_id}/search` | explicitly_unsupported_at_launch | -- | Search a vector store for relevant chunks based on a query and file attributes f |


## out_of_scope (51 endpoints)

| Method | Path | Status | Phase | Notes |
|--------|------|--------|-------|-------|
| GET | `/v1/organization/admin_api_keys` | out_of_scope | -- | List organization API keys |
| POST | `/v1/organization/admin_api_keys` | out_of_scope | -- | Create an organization admin API key |
| GET | `/v1/organization/admin_api_keys/{key_id}` | out_of_scope | -- | Retrieve a single organization API key |
| DELETE | `/v1/organization/admin_api_keys/{key_id}` | out_of_scope | -- | Delete an organization admin API key |
| GET | `/v1/organization/audit_logs` | out_of_scope | -- | List user actions and configuration changes within this organization. |
| GET | `/v1/organization/certificates` | out_of_scope | -- | List uploaded certificates for this organization. |
| POST | `/v1/organization/certificates` | out_of_scope | -- | Upload a certificate to the organization. This does **not** automatically activa |
| POST | `/v1/organization/certificates/activate` | out_of_scope | -- | Activate certificates at the organization level. |
| POST | `/v1/organization/certificates/deactivate` | out_of_scope | -- | Deactivate certificates at the organization level. |
| GET | `/v1/organization/certificates/{certificate_id}` | out_of_scope | -- | Get a certificate that has been uploaded to the organization. |
| POST | `/v1/organization/certificates/{certificate_id}` | out_of_scope | -- | Modify a certificate. Note that only the name can be modified. |
| DELETE | `/v1/organization/certificates/{certificate_id}` | out_of_scope | -- | Delete a certificate from the organization. |
| GET | `/v1/organization/costs` | out_of_scope | -- | Get costs details for the organization. |
| GET | `/v1/organization/invites` | out_of_scope | -- | Returns a list of invites in the organization. |
| POST | `/v1/organization/invites` | out_of_scope | -- | Create an invite for a user to the organization. The invite must be accepted by |
| GET | `/v1/organization/invites/{invite_id}` | out_of_scope | -- | Retrieves an invite. |
| DELETE | `/v1/organization/invites/{invite_id}` | out_of_scope | -- | Delete an invite. If the invite has already been accepted, it cannot be deleted. |
| GET | `/v1/organization/projects` | out_of_scope | -- | Returns a list of projects. |
| POST | `/v1/organization/projects` | out_of_scope | -- | Create a new project in the organization. Projects can be created and archived, |
| GET | `/v1/organization/projects/{project_id}` | out_of_scope | -- | Retrieves a project. |
| POST | `/v1/organization/projects/{project_id}` | out_of_scope | -- | Modifies a project in the organization. |
| GET | `/v1/organization/projects/{project_id}/api_keys` | out_of_scope | -- | Returns a list of API keys in the project. |
| GET | `/v1/organization/projects/{project_id}/api_keys/{key_id}` | out_of_scope | -- | Retrieves an API key in the project. |
| DELETE | `/v1/organization/projects/{project_id}/api_keys/{key_id}` | out_of_scope | -- | Deletes an API key from the project. |
| POST | `/v1/organization/projects/{project_id}/archive` | out_of_scope | -- | Archives a project in the organization. Archived projects cannot be used or upda |
| GET | `/v1/organization/projects/{project_id}/certificates` | out_of_scope | -- | List certificates for this project. |
| POST | `/v1/organization/projects/{project_id}/certificates/activate` | out_of_scope | -- | Activate certificates at the project level. |
| POST | `/v1/organization/projects/{project_id}/certificates/deactivate` | out_of_scope | -- | Deactivate certificates at the project level. |
| GET | `/v1/organization/projects/{project_id}/rate_limits` | out_of_scope | -- | Returns the rate limits per model for a project. |
| POST | `/v1/organization/projects/{project_id}/rate_limits/{rate_limit_id}` | out_of_scope | -- | Updates a project rate limit. |
| GET | `/v1/organization/projects/{project_id}/service_accounts` | out_of_scope | -- | Returns a list of service accounts in the project. |
| POST | `/v1/organization/projects/{project_id}/service_accounts` | out_of_scope | -- | Creates a new service account in the project. This also returns an unredacted AP |
| GET | `/v1/organization/projects/{project_id}/service_accounts/{service_account_id}` | out_of_scope | -- | Retrieves a service account in the project. |
| DELETE | `/v1/organization/projects/{project_id}/service_accounts/{service_account_id}` | out_of_scope | -- | Deletes a service account from the project. |
| GET | `/v1/organization/projects/{project_id}/users` | out_of_scope | -- | Returns a list of users in the project. |
| POST | `/v1/organization/projects/{project_id}/users` | out_of_scope | -- | Adds a user to the project. Users must already be members of the organization to |
| GET | `/v1/organization/projects/{project_id}/users/{user_id}` | out_of_scope | -- | Retrieves a user in the project. |
| POST | `/v1/organization/projects/{project_id}/users/{user_id}` | out_of_scope | -- | Modifies a user's role in the project. |
| DELETE | `/v1/organization/projects/{project_id}/users/{user_id}` | out_of_scope | -- | Deletes a user from the project. |
| GET | `/v1/organization/usage/audio_speeches` | out_of_scope | -- | Get audio speeches usage details for the organization. |
| GET | `/v1/organization/usage/audio_transcriptions` | out_of_scope | -- | Get audio transcriptions usage details for the organization. |
| GET | `/v1/organization/usage/code_interpreter_sessions` | out_of_scope | -- | Get code interpreter sessions usage details for the organization. |
| GET | `/v1/organization/usage/completions` | out_of_scope | -- | Get completions usage details for the organization. |
| GET | `/v1/organization/usage/embeddings` | out_of_scope | -- | Get embeddings usage details for the organization. |
| GET | `/v1/organization/usage/images` | out_of_scope | -- | Get images usage details for the organization. |
| GET | `/v1/organization/usage/moderations` | out_of_scope | -- | Get moderations usage details for the organization. |
| GET | `/v1/organization/usage/vector_stores` | out_of_scope | -- | Get vector stores usage details for the organization. |
| GET | `/v1/organization/users` | out_of_scope | -- | Lists all of the users in the organization. |
| GET | `/v1/organization/users/{user_id}` | out_of_scope | -- | Retrieves a user by their identifier. |
| POST | `/v1/organization/users/{user_id}` | out_of_scope | -- | Modifies a user's role in the organization. |
| DELETE | `/v1/organization/users/{user_id}` | out_of_scope | -- | Deletes a user from the organization. |
