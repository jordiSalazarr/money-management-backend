// Phase 4b adds Bedrock as the production backend for the voice slice
// and keeps the existing direct Anthropic-API path as a local-dev
// fallback. Both implement the same Backend interface so the Handler
// and Executor don't need to know which one is wired.

package voice

import (
	"context"
	"encoding/json"
)

// Backend is the LLM call surface the voice handler needs. Both
// implementations (Bedrock SDK, direct Anthropic API) satisfy it.
type Backend interface {
	Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error)
}

// InterpretResult is what we get back from the LLM. Either a tool was
// invoked (ToolName + ToolInput set) or the model produced text.
type InterpretResult struct {
	ToolName  string
	ToolInput json.RawMessage
	Text      string
}

// ClientOptions configures NewBackend. Empty fields fall back to env
// vars; if neither path resolves, NewBackend returns nil + error.
type ClientOptions struct {
	// BedrockModelID is the Bedrock-hosted Claude model identifier.
	// Empty falls back to the BEDROCK_MODEL_ID env var, then to a
	// sensible default baked into bedrock_backend.go.
	BedrockModelID string

	// AnthropicAPIKey, when non-empty, picks the direct Anthropic API
	// path. Useful for cmd/server local-dev mode without AWS creds.
	// Lambda mode leaves this empty and uses Bedrock.
	AnthropicAPIKey string
}

// NewBackend returns the right Backend implementation based on opts.
// Direct-Anthropic wins when the API key is set (explicit local-dev
// signal); otherwise Bedrock is used and credentials come from the
// default AWS chain.
func NewBackend(ctx context.Context, opts ClientOptions) (Backend, error) {
	if opts.AnthropicAPIKey != "" {
		return newAnthropicBackend(opts.AnthropicAPIKey), nil
	}
	return newBedrockBackend(ctx, opts.BedrockModelID)
}

// systemPromptTemplate is the system prompt sent on every interpret
// call. Lives at the package level so both backends format the same
// way; the %s is replaced by the per-user context (wallet names, debt
// list, etc.) the resolver builds before each call.
const systemPromptTemplate = `You are a money management voice assistant. The user speaks commands to manage their wallets.

Your job: figure out which tool to call based on what they said. Rules:

- Amounts are in integer cents. "50 euros" → amount: 5000, currency: "EUR". "20 dollars" → amount: 2000, currency: "USD".
- If the user doesn't specify a currency, default to "EUR".
- CRITICAL: The user's input comes from speech-to-text and may contain errors. Match wallet names to the closest name from the user's actual wallets listed below. Always use the EXACT wallet name from the list, not what was spoken.
- For direction (debts), "someone owes me" or "I lent" = "IN". "I owe someone" or "I borrowed" = "OUT".
- For recurring movement type: "deposit" or "withdrawal".
- For frequency: "DAILY", "WEEKLY", or "MONTHLY".
- For category, pick the closest: Cash, Saving, Investment, Food, Transport, Entertainment, Other.
- Always call a tool. If you truly cannot understand the command, respond with text explaining why.
- Never ask follow-up questions. Use your best judgment with the information given.

%s`
