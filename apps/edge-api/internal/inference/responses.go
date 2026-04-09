package inference

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// handleResponses handles POST /v1/responses.
func handleResponses(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeInvalidBodyError(w)
		return
	}

	var req ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidBodyError(w)
		return
	}

	if req.Model == "" {
		writeMissingFieldError(w, "model")
		return
	}
	if len(req.Input) == 0 {
		writeMissingFieldError(w, "input")
		return
	}

	// Translate ResponsesRequest to a chat/completions body for LiteLLM.
	chatBody, err := translateResponsesToChatCompletions(req)
	if err != nil {
		writeInvalidBodyError(w)
		return
	}

	needFlags := NeedFlags{
		NeedResponses: true,
		NeedStreaming:  req.Stream,
		NeedReasoning:  len(req.Reasoning) > 0 && string(req.Reasoning) != "null",
	}

	if req.Stream {
		o.executeResponsesStreaming(r.Context(), w, r, chatBody, req, req.Model, needFlags, 10000)
		return
	}

	// Sync path: executeSync with a normalizer that wraps into ResponseObject.
	aliasID := req.Model
	o.executeSync(
		r.Context(), w, r,
		EndpointResponses, chatBody, req.Model, needFlags, 10000,
		o.litellm.ChatCompletion,
		func(respBody []byte, _ string) ([]byte, *UsageResponse, error) {
			return normalizeResponsesSync(respBody, aliasID, req)
		},
	)
}

// translateResponsesToChatCompletions converts a ResponsesRequest into a JSON body
// suitable for sending to LiteLLM's /chat/completions endpoint.
func translateResponsesToChatCompletions(req ResponsesRequest) ([]byte, error) {
	// Build messages array.
	messages, err := buildMessagesFromInput(req)
	if err != nil {
		return nil, fmt.Errorf("translate input: %w", err)
	}

	body := map[string]json.RawMessage{}

	// Model (will be overwritten by LiteLLM dispatch layer).
	modelJSON, _ := json.Marshal(req.Model)
	body["model"] = modelJSON

	// Messages.
	msgsJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("marshal messages: %w", err)
	}
	body["messages"] = msgsJSON

	// stream.
	if req.Stream {
		body["stream"] = json.RawMessage(`true`)
		// Always request usage for Responses API streaming.
		body["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}

	// temperature.
	if req.Temperature != nil {
		v, _ := json.Marshal(req.Temperature)
		body["temperature"] = v
	}

	// top_p.
	if req.TopP != nil {
		v, _ := json.Marshal(req.TopP)
		body["top_p"] = v
	}

	// max_completion_tokens from max_output_tokens.
	if req.MaxOutputTokens != nil {
		v, _ := json.Marshal(req.MaxOutputTokens)
		body["max_completion_tokens"] = v
	}

	// response_format from text.format.
	if len(req.Text) > 0 && string(req.Text) != "null" {
		var textObj map[string]json.RawMessage
		if err := json.Unmarshal(req.Text, &textObj); err == nil {
			if format, ok := textObj["format"]; ok {
				body["response_format"] = format
			}
		}
	}

	// reasoning_effort from reasoning.effort.
	if len(req.Reasoning) > 0 && string(req.Reasoning) != "null" {
		var reasoningObj map[string]json.RawMessage
		if err := json.Unmarshal(req.Reasoning, &reasoningObj); err == nil {
			if effort, ok := reasoningObj["effort"]; ok {
				body["reasoning_effort"] = effort
			}
		}
	}

	// tools: translate from Responses API format to chat/completions format.
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		translated, err := translateToolsToChat(req.Tools)
		if err == nil {
			body["tools"] = translated
		}
	}

	// tool_choice.
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		body["tool_choice"] = req.ToolChoice
	}

	// user.
	if req.User != nil {
		v, _ := json.Marshal(req.User)
		body["user"] = v
	}

	return json.Marshal(body)
}

// buildMessagesFromInput converts a ResponsesRequest Input and Instructions into a chat messages array.
func buildMessagesFromInput(req ResponsesRequest) ([]map[string]any, error) {
	var messages []map[string]any

	// Prepend system message if instructions provided.
	if req.Instructions != nil && *req.Instructions != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": *req.Instructions,
		})
	}

	// Input can be a string or an array of messages.
	input := req.Input
	trimmed := string(input)
	if len(trimmed) == 0 {
		return messages, nil
	}

	// Try parsing as string.
	var strInput string
	if err := json.Unmarshal(input, &strInput); err == nil {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": strInput,
		})
		return messages, nil
	}

	// Try parsing as array of message objects.
	var arrInput []json.RawMessage
	if err := json.Unmarshal(input, &arrInput); err == nil {
		for _, item := range arrInput {
			var msg map[string]any
			if err := json.Unmarshal(item, &msg); err != nil {
				return nil, fmt.Errorf("invalid message in input array: %w", err)
			}
			messages = append(messages, msg)
		}
		return messages, nil
	}

	return nil, fmt.Errorf("input must be a string or array of messages")
}

// translateToolsToChat converts Responses API tools format to chat/completions tools format.
// Responses API: {type:"function", name:"...", description:"...", parameters:{...}, strict:true}
// Chat API:      {type:"function", function:{name:"...", description:"...", parameters:{...}, strict:true}}
func translateToolsToChat(toolsRaw json.RawMessage) (json.RawMessage, error) {
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		return toolsRaw, nil // pass through on failure
	}

	chatTools := make([]map[string]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		toolType, ok := tool["type"]
		if !ok {
			chatTools = append(chatTools, tool)
			continue
		}

		var typeStr string
		if err := json.Unmarshal(toolType, &typeStr); err != nil || typeStr != "function" {
			chatTools = append(chatTools, tool)
			continue
		}

		// Build function body from top-level fields.
		fnBody := map[string]json.RawMessage{}
		for _, field := range []string{"name", "description", "parameters", "strict"} {
			if v, exists := tool[field]; exists {
				fnBody[field] = v
			}
		}

		fnJSON, err := json.Marshal(fnBody)
		if err != nil {
			chatTools = append(chatTools, tool)
			continue
		}

		chatTools = append(chatTools, map[string]json.RawMessage{
			"type":     toolType,
			"function": fnJSON,
		})
	}

	return json.Marshal(chatTools)
}

// normalizeResponsesSync converts a LiteLLM chat completion response into a ResponseObject.
func normalizeResponsesSync(respBody []byte, aliasID string, req ResponsesRequest) ([]byte, *UsageResponse, error) {
	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, nil, fmt.Errorf("parse chat completion: %w", err)
	}

	responseID := "resp_" + uuid.New().String()
	now := time.Now().Unix()
	createdAt := chatResp.Created
	if createdAt == 0 {
		createdAt = now
	}

	// Build output items from choices.
	outputItems := buildOutputItemsFromChoices(chatResp.Choices)

	// Translate usage.
	var respUsage *ResponsesUsage
	if chatResp.Usage != nil {
		respUsage = chatToResponsesUsage(chatResp.Usage)
	}

	nullJSON := json.RawMessage(`null`)
	emptyTools := json.RawMessage(`[]`)
	truncation := "disabled"

	resp := ResponseObject{
		ID:                responseID,
		Object:            "response",
		CreatedAt:         createdAt,
		Model:             aliasID,
		Status:            "completed",
		Output:            outputItems,
		Usage:             respUsage,
		Reasoning:         nullJSON,
		Metadata:          nullJSON,
		MaxOutputTokens:   req.MaxOutputTokens,
		Truncation:        &truncation,
		Tools:             emptyTools,
		IncompleteDetails: nullJSON,
		Error:             nullJSON,
	}

	// Copy optional fields from request.
	resp.Temperature = req.Temperature
	resp.TopP = req.TopP

	// text field passthrough.
	if len(req.Text) > 0 && string(req.Text) != "null" {
		resp.Text = req.Text
	}

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal response: %w", err)
	}

	return normalized, chatResp.Usage, nil
}

// buildOutputItemsFromChoices converts chat completion choices into Responses API output items.
func buildOutputItemsFromChoices(choices []ChatCompletionChoice) []ResponseOutputItem {
	items := make([]ResponseOutputItem, 0, len(choices))
	for _, choice := range choices {
		msgID := "msg_" + uuid.New().String()
		var content []ResponseContentPart

		if choice.Message.Content != nil && *choice.Message.Content != "" {
			content = append(content, ResponseContentPart{
				Type:        "output_text",
				Text:        *choice.Message.Content,
				Annotations: []json.RawMessage{},
			})
		}

		if len(content) == 0 {
			content = []ResponseContentPart{}
		}

		items = append(items, ResponseOutputItem{
			Type:    "message",
			ID:      msgID,
			Status:  "completed",
			Role:    "assistant",
			Content: content,
		})
	}
	return items
}

// chatToResponsesUsage translates a chat-completions UsageResponse to Responses API ResponsesUsage.
func chatToResponsesUsage(u *UsageResponse) *ResponsesUsage {
	ru := &ResponsesUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.CompletionTokensDetails != nil {
		ru.OutputTokensDetails = &OutputTokensDetails{
			ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens,
		}
	}
	if u.PromptTokensDetails != nil {
		ru.InputTokensDetails = &InputTokensDetails{
			CachedTokens: u.PromptTokensDetails.CachedTokens,
		}
	}
	return ru
}

// responsesUsageToChat converts a ResponsesUsage to UsageResponse for accounting.
func responsesUsageToChat(u *ResponsesUsage) *UsageResponse {
	if u == nil {
		return nil
	}
	result := &UsageResponse{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.OutputTokensDetails != nil {
		result.CompletionTokensDetails = &CompletionTokensDetails{
			ReasoningTokens: u.OutputTokensDetails.ReasoningTokens,
		}
	}
	if u.InputTokensDetails != nil {
		result.PromptTokensDetails = &PromptTokensDetails{
			CachedTokens: u.InputTokensDetails.CachedTokens,
		}
	}
	return result
}
