package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client calls the Claude API with tool use to interpret voice commands.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		model:      "claude-sonnet-4-6",
		httpClient: http.DefaultClient,
	}
}

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

// --- Request types ---

type claudeRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Tools     []Tool           `json:"tools"`
	Messages  []claudeMessage  `json:"messages"`
}

type claudeMessage struct {
	Role    string               `json:"role"`
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// --- Response types ---

type claudeResponse struct {
	Content  []claudeContentBlock `json:"content"`
	StopReason string             `json:"stop_reason"`
}

// InterpretResult is what we get back from Claude.
type InterpretResult struct {
	// ToolUse is set when Claude called a tool.
	ToolName  string
	ToolInput json.RawMessage
	// Text is set when Claude responded with text instead of a tool call.
	Text string
}

// Interpret sends user text to Claude with tools and returns the interpreted result.
// userContext contains the user's wallets, debts, etc. for accurate name matching.
func (c *Client) Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error) {
	system := fmt.Sprintf(systemPromptTemplate, userContext)

	reqBody := claudeRequest{
		Model:     c.model,
		MaxTokens: 1024,
		System:    system,
		Tools:     tools,
		Messages: []claudeMessage{
			{
				Role: "user",
				Content: []claudeContentBlock{
					{Type: "text", Text: text},
				},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling claude request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating claude request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling claude API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading claude response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, fmt.Errorf("unmarshaling claude response: %w", err)
	}

	// Look for a tool_use block first.
	for _, block := range claudeResp.Content {
		if block.Type == "tool_use" {
			return &InterpretResult{
				ToolName:  block.Name,
				ToolInput: block.Input,
			}, nil
		}
	}

	// Fall back to text response.
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			return &InterpretResult{Text: block.Text}, nil
		}
	}

	return nil, fmt.Errorf("claude returned no content")
}
