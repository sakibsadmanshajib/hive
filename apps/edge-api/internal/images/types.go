package images

// ImageGenerationRequest is the OpenAI-compatible image generation request.
type ImageGenerationRequest struct {
	Model          string  `json:"model"`
	Prompt         string  `json:"prompt"`
	N              *int    `json:"n,omitempty"`
	Size           *string `json:"size,omitempty"`
	Quality        *string `json:"quality,omitempty"`
	Style          *string `json:"style,omitempty"`
	ResponseFormat *string `json:"response_format,omitempty"` // "url" or "b64_json"
	User           *string `json:"user,omitempty"`
}

// ImageResponse is the OpenAI-compatible image response envelope.
type ImageResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
}

// ImageData is a single image in an image response.
type ImageData struct {
	URL           *string `json:"url,omitempty"`
	B64JSON       *string `json:"b64_json,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}
