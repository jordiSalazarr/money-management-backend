# AWS Migration Plan — Phase 4b (Bedrock for voice)

**Status**: SHIPPED — backend `59a88c6`, infra `d255162`. One note below supersedes the original plan.
**Last updated**: 2026-06-09
**Reviewers**: jordi
**Phase**: 4b — voice slice in-VPC
**Repos in scope**: `backend`, `money-management-infra`.

> **Update (post-merge): AWS retired the Bedrock "Model access" Console page.** Serverless foundation models auto-enable on first invocation, so the manual toggle described in §3.4 / §7 / §9 Q5 is obsolete. The only residual human step: if your AWS account has never used Anthropic models on Bedrock before, the first `Converse` call returns a "submit use case details" error with a link to a one-time form — fill it out and retry. IAM still gates access via the `bedrock_invoke` policy (unchanged).

Phase 2-3 stood up the VPC with no NAT and three interface endpoints (kinesis-streams, secretsmanager, logs). Phase 4a made the payments Lambda live in that VPC. The voice slice has been off in prod ever since because it calls `https://api.anthropic.com/v1/messages` directly — public internet, unreachable without NAT.

This phase replaces the direct Anthropic call with AWS Bedrock's `Converse` API. Bedrock has a VPC interface endpoint, so the call stays inside AWS networking; voice works in prod without adding a NAT.

---

## 1. Context

The current voice slice (`internal/voice/claude.go`):

- Builds a Claude request with system prompt + 8 tool definitions + the user's text.
- POSTs it to `https://api.anthropic.com/v1/messages` with `x-api-key`.
- Parses the response for a `tool_use` block, falls back to text.

Bedrock's `Converse` API takes a near-identical request shape (system, messages, tools) and returns the same kind of content blocks. The SDK wraps the JSON and handles SigV4 signing.

## 2. Goal

End-state after merge + apply + deploy:

- A `com.amazonaws.eu-west-1.bedrock-runtime` interface endpoint exists in the VPC.
- The payments Lambda's role has `bedrock:InvokeModel` (and `bedrock:Converse` for the newer API) on the chosen Claude model ARN.
- `internal/voice/claude.go` calls Bedrock via the SDK instead of the public API.
- `ANTHROPIC_API_KEY` env var is no longer required; the Lambda authenticates to Bedrock via its IAM role.
- Setting it on local cmd/server still works as a fallback for development without AWS creds — preserved for friction-free local iteration.
- The voice slice (`/voice/command`) responds correctly in prod.

## 3. Architectural shifts

### 3.1 Direct HTTP → Bedrock SDK

Replace the raw `http.Client.Do` against `api.anthropic.com` with a `bedrockruntime.Client` and the SDK's `Converse` action. Same Claude model family, same tool-use mechanics — Bedrock is just an authenticated proxy in front of Anthropic's inference layer.

The SDK uses the same SigV4 path everything else does. Within VPC + interface endpoint, the call routes via the endpoint's ENI rather than the (nonexistent) NAT.

### 3.2 Backend client surface (small public-API change)

`voice.NewClient(apiKey string)` becomes `voice.NewClient(opts ClientOptions)` where:

```go
type ClientOptions struct {
    // BedrockModelID is the Bedrock-hosted Claude model. Empty defaults to
    // the env var BEDROCK_MODEL_ID, then to a sensible Claude model.
    BedrockModelID string

    // AnthropicAPIKey, if non-empty, makes the client use the direct
    // Anthropic API path instead of Bedrock. Only useful in cmd/server
    // local-dev mode without AWS credentials. Lambda mode leaves it empty.
    AnthropicAPIKey string
}
```

Lambda path: read no env vars about API keys, build Bedrock client, call `Converse`.
Local cmd/server path: if `ANTHROPIC_API_KEY` is set, use the direct API; otherwise try Bedrock (with whatever local AWS creds exist).

### 3.3 IAM scoping

Phase 2's IAM patterns use named managed policies attached via `additional_policy_arns`. Bedrock follows the same pattern:

```hcl
resource "aws_iam_policy" "bedrock_invoke" {
  name = "mm-bedrock-invoke-dev"
  policy = data.aws_iam_policy_document.bedrock_invoke.json
}

data "aws_iam_policy_document" "bedrock_invoke" {
  statement {
    actions = ["bedrock:InvokeModel", "bedrock:Converse"]
    resources = [
      "arn:aws:bedrock:eu-west-1::foundation-model/anthropic.claude-3-5-sonnet-*",
      "arn:aws:bedrock:eu-west-1:${data.aws_caller_identity.current.account_id}:inference-profile/*"
    ]
  }
}
```

Attached to the payments Lambda's `additional_policy_arns` map.

### 3.4 Manual step — Bedrock model access

Bedrock requires model access to be granted via the AWS console (Bedrock → Model access → Manage model access). Terraform can't do this. We have to flip the toggle for the chosen Claude model in eu-west-1 before the call works. One-time, takes ~30 seconds, no waiting.

---

## 4. Concrete changes

### 4.1 `backend`

| Path | Change |
|---|---|
| `internal/voice/claude.go` | Rewrite. Two backends: Bedrock (default) + direct Anthropic API (fallback when AnthropicAPIKey is set). Same `Interpret` signature. |
| `internal/voice/claude_test.go` | New. Hermetic test using a fake Bedrock client. Existing voice handler tests stay green. |
| `internal/voice/register.go` | Adjust `RegisterRoutes` signature: take `ClientOptions` instead of `apiKey string`. |
| `internal/app/engine.go` | Read `BEDROCK_MODEL_ID` + `ANTHROPIC_API_KEY` env vars; build `ClientOptions`; voice slice opt-in flips based on whether either path is configured. |
| `go.mod` / `go.sum` | Add `github.com/aws/aws-sdk-go-v2/service/bedrockruntime`. |

### 4.2 `money-management-infra`

| Path | Change |
|---|---|
| `modules/vpc/endpoints.tf` | Add `bedrock-runtime` to `interface_endpoint_services`. |
| `envs/dev/bedrock.tf` (new) | `aws_iam_policy.bedrock_invoke` + the policy document. |
| `envs/dev/main.tf` | Add `bedrock_invoke = aws_iam_policy.bedrock_invoke.arn` to payments Lambda's `additional_policy_arns`. |

No new lambdas, no schedule changes, no Aurora touch.

---

## 5. Implementation details

### 5.1 Bedrock SDK call

```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
    bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func (c *bedrockClient) Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error) {
    system := []bedrocktypes.SystemContentBlock{
        &bedrocktypes.SystemContentBlockMemberText{Value: fmt.Sprintf(systemPromptTemplate, userContext)},
    }

    out, err := c.client.Converse(ctx, &bedrockruntime.ConverseInput{
        ModelId:    aws.String(c.modelID),
        System:     system,
        ToolConfig: buildToolConfig(tools),
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

    for _, block := range msg.Value.Content {
        switch b := block.(type) {
        case *bedrocktypes.ContentBlockMemberToolUse:
            input, _ := json.Marshal(b.Value.Input)
            return &InterpretResult{ToolName: aws.ToString(b.Value.Name), ToolInput: input}, nil
        case *bedrocktypes.ContentBlockMemberText:
            return &InterpretResult{Text: b.Value}, nil
        }
    }
    return nil, fmt.Errorf("bedrock returned no usable content")
}
```

Tool definitions need light reshaping for the Bedrock format — Bedrock wants `inputSchema` keyed under `json` with the raw schema; Anthropic's API takes it inline. Small adapter:

```go
func buildToolConfig(tools []Tool) *bedrocktypes.ToolConfiguration {
    btools := make([]bedrocktypes.Tool, 0, len(tools))
    for _, t := range tools {
        var schemaDoc map[string]any
        _ = json.Unmarshal(t.InputSchema, &schemaDoc)
        btools = append(btools, &bedrocktypes.ToolMemberToolSpec{
            Value: bedrocktypes.ToolSpecification{
                Name:        aws.String(t.Name),
                Description: aws.String(t.Description),
                InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{
                    Value: document.NewLazyDocument(schemaDoc),
                },
            },
        })
    }
    return &bedrocktypes.ToolConfiguration{Tools: btools}
}
```

### 5.2 Anthropic API fallback (preserved for local dev)

Keep the existing HTTP-against-Anthropic path behind `ClientOptions.AnthropicAPIKey`. Move it into `internal/voice/anthropic.go` as a second backend implementing the same `Interpret` interface.

```go
type Backend interface {
    Interpret(ctx context.Context, text string, tools []Tool, userContext string) (*InterpretResult, error)
}

func NewClient(opts ClientOptions) (Backend, error) {
    if opts.AnthropicAPIKey != "" {
        return newAnthropicBackend(opts.AnthropicAPIKey), nil
    }
    return newBedrockBackend(opts.BedrockModelID)
}
```

### 5.3 Model ID — recommendation

For dev: **`anthropic.claude-3-5-haiku-20241022-v1:0`** (Bedrock). Cheap (~$0.80 / $4 per million in / out tokens), fast, good enough at tool-use for our 8-tool surface. Sonnet 4 is the right call when accuracy matters in prod, but for the toy project the latency + cost win matters more.

Model ID lives in the `BEDROCK_MODEL_ID` env var on the Lambda. Default-baked into `voice.NewClient` for missing env. Bedrock takes either the bare model ID or a cross-region inference profile ARN — we'll use the model ID for simplicity.

### 5.4 Bedrock VPC endpoint

```hcl
# modules/vpc/endpoints.tf — extend interface_endpoint_services:
locals {
  interface_endpoint_services = toset([
    "kinesis-streams",
    "secretsmanager",
    "logs",
    "bedrock-runtime",  # new
  ])
}
```

Cost: +$14/mo for the bedrock-runtime endpoint across 2 AZs. Brings the interface-endpoint total to ~$56/mo.

---

## 6. Verification

After apply + deploy:

```sh
# Sign in via the web app, then:
TOKEN=$(...)  # ID token from Cognito

curl -fsS -X POST "$API/voice/command" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"text":"deposit 50 EUR into my savings wallet"}'

# expect: a JSON envelope describing the tool that Claude picked
# (deposit_money) with amount, currency, wallet_id resolved by the
# resolver. If you see "claude returned no content" check Bedrock
# model access in the AWS console.
```

CloudWatch logs from the payments Lambda should show a successful Bedrock Converse call.

---

## 7. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Bedrock model access not granted in the console | High (first deploy) | High (voice 500s) | Document the manual step. Plan calls it out (§3.4). |
| Bedrock returns different tool-use shape that breaks the parser | Low | Med | Hermetic test with captured fixture. Both backends share `InterpretResult`. |
| Bedrock quotas (default 5 TPS per model) get hit | Very low | Med | We have 1 user. Quota irrelevant. Document for future. |
| Bedrock endpoint cost surprises | Low | Low | $14/mo flat. Tracked in cost anomaly (Phase 5). |
| Local cmd/server users without AWS creds break | Med | Low | Anthropic-API fallback preserved for the `ANTHROPIC_API_KEY` env var. |

---

## 8. Out of scope

- Switching voice from speech-to-text input to direct text (already text-in today).
- Streaming responses (Bedrock supports `ConverseStream` but the slice is request/response).
- Cross-region inference profiles (single-region eu-west-1 is fine).
- Bedrock guardrails / PII filtering.
- Multi-model fallback (Sonnet → Haiku on error).

---

## 9. Open questions for review

1. **Default model: `anthropic.claude-3-5-haiku-20241022-v1:0`** for cheap + fast tool use. Y/N?
2. **Preserve Anthropic-API fallback** behind `ClientOptions.AnthropicAPIKey` for local-dev friction-free iteration. Y/N?
3. **Add `bedrock-runtime` to the interface endpoint set** (+$14/mo). Y/N?
4. **`bedrock:InvokeModel` + `bedrock:Converse`** actions scoped to the `anthropic.claude-3-5-haiku-*` foundation-model ARN. Y/N?
5. **Manual "Model access" toggle in the AWS console** is part of the deploy runbook, not Terraform. Y/N?

---

**End of plan. Y/N each in §9 and I'll execute.**
