// Transcriber is the audio→text surface the voice handler needs.
//
// Mirrors the Backend interface pattern: one implementation today
// (AWS Transcribe Streaming) but the seam is here so a stub or a
// different provider can swap in without touching the handler.

package voice

import "context"

// Transcriber converts a WAV blob into a text transcript.
//
// wav is expected to be a complete RIFF/WAVE PCM 16-bit mono file.
// The implementation parses the header to read the sample rate so the
// frontend can record at whatever rate getUserMedia produces (typically
// 16 kHz once the WAV recorder downsamples for Transcribe).
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte) (string, error)
}

// TranscriberOptions configures NewTranscriber. Kept separate from
// ClientOptions so the LLM and STT backends evolve independently.
type TranscriberOptions struct {
	// LanguageCode passed to Transcribe (e.g. "en-US"). Empty falls
	// back to "en-US" inside the backend.
	LanguageCode string
}

// NewTranscriber returns the production Transcribe backend. The Lambda
// authenticates via its IAM role and reaches the service through the
// VPC interface endpoint added in infra.
func NewTranscriber(ctx context.Context, opts TranscriberOptions) (Transcriber, error) {
	return newAWSTranscribeBackend(ctx, opts.LanguageCode)
}
