// Bedrock backend — production path for the voice slice.
//
// Calls Claude via AWS Bedrock's Converse API. The Lambda authenticates
// via its IAM role (no API key); credentials come from the default AWS
// chain. The VPC interface endpoint added in phase 4b infra routes the
// call inside AWS networking, so no NAT is required.

package voice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// defaultBedrockModelID — Claude 3.5 Haiku via Bedrock. Cheap + fast +
// good enough at tool use for the 8-tool surface. Override via
// BEDROCK_MODEL_ID env var (read by internal/app/engine.go) or via
// ClientOptions.BedrockModelID.
const defaultBedrockModelID = "anthropic.claude-3-5-haiku-20241022-v1:0"

type bedrockBackend struct {
	client  *bedrockruntime.Client
	modelID string
}

func newBedrockBackend(ctx context.Context, modelID string) (*bedrockBackend, error) {
	if modelID == "" {
		modelID = defaultBedrockModelID
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading aws config for bedrock: %w", err)
	}
	return &bedrockBackend{
		client:  bedrockruntime.NewFromConfig(cfg),
		modelID: modelID,
	}, nil
}

func (b *bedrockBackend) Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error) {
	system := []bedrocktypes.SystemContentBlock{
		&bedrocktypes.SystemContentBlockMemberText{Value: fmt.Sprintf(systemPromptTemplate, userContext)},
	}

	toolConfig, err := buildBedrockToolConfig(tools)
	if err != nil {
		return nil, fmt.Errorf("building tool config: %w", err)
	}

	out, err := b.client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId:    aws.String(b.modelID),
		System:     system,
		ToolConfig: toolConfig,
		Messages: []bedrocktypes.Message{{
			Role: bedrocktypes.ConversationRoleUser,
			Content: []bedrocktypes.ContentBlock{
				&bedrocktypes.ContentBlockMemberText{Value: text},
			},
		}},
		InferenceConfig: &bedrocktypes.InferenceConfiguration{
			MaxTokens: aws.Int32(1024),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock Converse: %w", err)
	}

	msg, ok := out.Output.(*bedrocktypes.ConverseOutputMemberMessage)
	if !ok {
		return nil, fmt.Errorf("unexpected Converse output type %T", out.Output)
	}

	// Prefer a tool_use block; fall back to text. Mirrors the Anthropic
	// backend's precedence so the rest of the slice handles them the
	// same way.
	for _, block := range msg.Value.Content {
		if tu, ok := block.(*bedrocktypes.ContentBlockMemberToolUse); ok {
			raw, err := marshalToolInput(tu.Value.Input)
			if err != nil {
				return nil, fmt.Errorf("marshalling tool input: %w", err)
			}
			return &InterpretResult{
				ToolName:  aws.ToString(tu.Value.Name),
				ToolInput: raw,
			}, nil
		}
	}
	for _, block := range msg.Value.Content {
		if t, ok := block.(*bedrocktypes.ContentBlockMemberText); ok {
			return &InterpretResult{Text: t.Value}, nil
		}
	}
	return nil, fmt.Errorf("bedrock returned no usable content")
}

// buildBedrockToolConfig adapts our flat Tool struct (matching the
// Anthropic API shape) into Bedrock's nested Tool/ToolSpecification/
// ToolInputSchema unions. The schema JSON is the same; we just wrap it.
func buildBedrockToolConfig(tools []Tool) (*bedrocktypes.ToolConfiguration, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	bt := make([]bedrocktypes.Tool, 0, len(tools))
	for _, t := range tools {
		var schemaDoc any
		if err := json.Unmarshal(t.InputSchema, &schemaDoc); err != nil {
			return nil, fmt.Errorf("decoding %s input schema: %w", t.Name, err)
		}
		bt = append(bt, &bedrocktypes.ToolMemberToolSpec{
			Value: bedrocktypes.ToolSpecification{
				Name:        aws.String(t.Name),
				Description: aws.String(t.Description),
				InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(schemaDoc),
				},
			},
		})
	}
	return &bedrocktypes.ToolConfiguration{Tools: bt}, nil
}

// marshalToolInput turns Bedrock's document.Interface (an opaque
// any-shaped JSON value) into json.RawMessage so the rest of the voice
// slice — which was built against the Anthropic shape — can pass it
// through unchanged.
func marshalToolInput(doc document.Interface) (json.RawMessage, error) {
	var v any
	if err := doc.UnmarshalSmithyDocument(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
