package audio

// SpeechRequest is the OpenAI-compatible TTS request.
type SpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat *string  `json:"response_format,omitempty"` // mp3, opus, aac, flac, wav, pcm
	Speed          *float64 `json:"speed,omitempty"`
}

// TranscriptionResponse is the OpenAI-compatible transcription response.
type TranscriptionResponse struct {
	Text     string                 `json:"text"`
	Task     *string                `json:"task,omitempty"`
	Language *string                `json:"language,omitempty"`
	Duration *float64               `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// TranscriptionSegment is a single segment in a transcription response.
type TranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

// TranslationResponse is the OpenAI-compatible translation response.
type TranslationResponse struct {
	Text     string   `json:"text"`
	Task     *string  `json:"task,omitempty"`
	Language *string  `json:"language,omitempty"`
	Duration *float64 `json:"duration,omitempty"`
}
