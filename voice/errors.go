package voice

import "errors"

var (
	ErrMissingText   = errors.New("text is required")
	ErrMissingAPIKey = errors.New("ANTHROPIC_API_KEY is not set")
)
