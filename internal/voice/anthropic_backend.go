// Direct Anthropic API path. Preserved from the pre-phase-4b code so
// local cmd/server iteration without AWS credentials still works — set
// ANTHROPIC_API_KEY and the slice uses this backend instead of Bedrock.
//
// The deployed Lambda never uses this path: the no-NAT VPC has no
// route to api.anthropic.com. The infra deploys with the env var
// unset; bedrock_backend.go handles prod.

package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type anthropicBackend struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func newAnthropicBackend(apiKey string) *anthropicBackend {
	return &anthropicBackend{
		apiKey:     apiKey,
		model:      "claude-sonnet-4-6",
		httpClient: http.DefaultClient,
	}
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Tools     []Tool          `json:"tools"`
	Messages  []claudeMessage `json:"messages"`
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

type claudeResponse struct {
	Content    []claudeContentBlock `json:"content"`
	StopReason string               `json:"stop_reason"`
}

func (c *anthropicBackend) Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error) {
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
		return nil, fmt.Errorf("marshaling anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading anthropic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, fmt.Errorf("unmarshaling anthropic response: %w", err)
	}

	for _, block := range claudeResp.Content {
		if block.Type == "tool_use" {
			return &InterpretResult{
				ToolName:  block.Name,
				ToolInput: block.Input,
			}, nil
		}
	}
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			return &InterpretResult{Text: block.Text}, nil
		}
	}
	return nil, fmt.Errorf("anthropic returned no content")
}
