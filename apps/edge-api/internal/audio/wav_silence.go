package audio

import "encoding/binary"

// wavDataChunk locates and returns the sample data ("data" subchunk) of a
// RIFF/WAVE file. It returns ok=false for anything that isn't a WAVE file
// (including compressed formats such as mp3/opus/aac, which this package
// never attempts to silence-check) so callers can skip validation entirely
// for non-WAV responses.
func wavDataChunk(b []byte) (data []byte, ok bool) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, false
	}
	pos := 12
	for pos+8 <= len(b) {
		chunkID := string(b[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		dataStart := pos + 8
		if chunkID == "data" {
			end := dataStart + chunkSize
			if end > len(b) || chunkSize < 0 {
				end = len(b)
			}
			if dataStart > end {
				return nil, false
			}
			return b[dataStart:end], true
		}
		pos = dataStart + chunkSize
		if chunkSize%2 == 1 { // RIFF subchunks are word-aligned
			pos++
		}
	}
	return nil, false
}

// isDegenerateSilence reports whether WAV sample data is silence rather than
// real audio content. Confirmed live defect: Groq's Orpheus TTS backend
// (reached directly or through LiteLLM's route-groq-tts -- both were tested
// live and behave identically) intermittently returns HTTP 200 with a
// correctly-shaped WAV file whose PCM sample data is entirely zero, instead
// of the requested speech. A real spoken sentence is never anywhere close to
// all-zero at the byte level, so a strict threshold has no false positives
// against genuine (even quiet) audio.
func isDegenerateSilence(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	nonZero := 0
	for _, v := range data {
		if v != 0 {
			nonZero++
		}
	}
	return float64(nonZero)/float64(len(data)) < 0.001
}
