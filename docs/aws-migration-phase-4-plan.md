# AWS Migration Plan — Phase 4 (Cognito + frontend deployment)

**Status**: Draft for review
**Last updated**: 2026-06-09
**Reviewers**: jordi
**Phase**: 4 of 4 — sub-phases 4a (auth) + 4c (frontend). 4b (Bedrock for voice) and 4d (Secrets Manager rotation) explicitly deferred.
**Repos in scope**: `backend`, `money-management-infra`, `web`.

Phase 1 (`08e0045`), Phase 2 (`26fd9f3`), Phase 3 (`e9c633a`) have all landed. Each was matched by an infra PR. Phase 4 cuts auth over to Cognito and stands up the public-facing frontend, replacing the Docker/nginx path.

---

## 1. Context

Today, after Phases 1-3, the system is fully event-sourced on AWS but still uses **custom JWT auth**: the `auth/register` and `auth/login` slices issue tokens signed with `JWT_SECRET`, the user records live in a dedicated DDB table (`mm-users-dev`), and the backend's `middleware.Auth` validates the HMAC signature on every request. The frontend exists in the `web/` repo (Vite + React SPA) but is **not deployed** — it ships via a `Dockerfile` + `nginx.conf` that isn't part of the AWS pipeline.

Phase 4 closes both gaps:

- **4a — Cognito as the identity provider.** API Gateway HTTP API has a native JWT authorizer that validates tokens against any OIDC issuer's JWKS — including a Cognito user pool. The Lambda receives validated claims in the API GW event; the backend's middleware stops validating signatures and just reads `RequestContext.Authorizer.JWT.Claims["sub"]` for the user ID. `auth/register` + `auth/login` slices, `auth/jwt.go`, the `UserStorer` interface and its DDB/Memory impls, the `mm-users-dev` table, and `JWT_SECRET` all go away.
- **4c — Frontend on S3 + CloudFront.** The Vite production bundle (`web/dist/`) syncs to a versioned S3 bucket; CloudFront serves it through a TLS distribution with an Origin Access Control (OAC) pointing at the bucket. CORS on API GW allows the CloudFront distribution domain.

What stays deferred:

- **4b — Bedrock for voice.** Voice slice stays off in prod until/unless you want it. Direct `api.anthropic.com` reach requires NAT (deferred), or swap the backend client to Bedrock SDK + add a Bedrock VPC endpoint. Phase 4 doesn't touch it.
- **4d — Secrets Manager rotation.** `AURORA_URL` still injected at TF apply time. Phase 5 (observability uplift) is the natural home; not blocking anything.

## 2. Goal of Phase 4 (4a + 4c)

End-state after merge + apply:

- A Cognito user pool `mm-users-dev` exists with a public-client app (PKCE, no client secret) and a hosted UI domain.
- API GW route `ANY /{proxy+}` has a JWT authorizer attached, validating against the Cognito issuer URL.
- The payments Lambda's middleware reads `sub` from the API GW JWT claims, not from a custom-validated token.
- `JWT_SECRET` env var, the `mm-users-dev` DDB table, the auth/login + auth/register routes, and `auth.UserStorer` are all gone.
- `web/` builds to `dist/` and deploys to a `mm-web-dev` S3 bucket; CloudFront serves it at a stable URL.
- The frontend sign-in flow uses the Cognito Hosted UI; once authenticated, it carries the Cognito ID token on every API request.
- API GW CORS allows the CloudFront origin.

What stays the same:

- Same gin engine, same vertical slices for everything past `/auth/*`.
- Same Aurora + Kinesis + reactors + cron.
- Same backend deploy pipeline (CI ships Lambdas).

---

## 3. Architectural shifts

### 3.1 Cognito + API GW JWT authorizer

API GW HTTP API JWT authorizers are configured with an issuer URL + audience. For Cognito, the issuer is `https://cognito-idp.eu-west-1.amazonaws.com/<user-pool-id>` and the audience is the app client ID. The gateway fetches the JWKS at startup, caches it, and validates every incoming `Authorization: Bearer <token>` automatically. Failed tokens get 401 before the Lambda ever sees the request.

Validated claims arrive in the Lambda event at `req.RequestContext.Authorizer.JWT.Claims`. The backend's `cmd/lambda` already uses the `awslabs/aws-lambda-go-api-proxy/gin` adapter — the adapter stuffs the original event into the request context so middleware can read it. New `middleware.CognitoAuth` does exactly that.

Cognito user IDs are UUID `sub` claims. Today's backend `UserID` field is a UUID string. No format change needed at the aggregate level.

### 3.2 Frontend deployment (S3 + CloudFront)

Standard SPA hosting pattern:

- S3 bucket `mm-web-dev`, **private** (block-public-access enabled). CloudFront reaches it via Origin Access Control.
- CloudFront distribution with `index.html` as default root object. Error responses for 403 + 404 rewrite to `/index.html` so React Router handles client-side routes after a hard refresh.
- ACM cert in **us-east-1** (CloudFront's hard requirement) for HTTPS. No custom domain in Phase 4 — the CloudFront default `xxxx.cloudfront.net` is fine for dev. Custom domain + Route53 record is a follow-up.

Deploy from CI:

- `npm run build` produces `dist/`.
- `aws s3 sync dist/ s3://mm-web-dev/ --delete` (no `--cache-control` games for now — defer cache-busting to a follow-up).
- `aws cloudfront create-invalidation --distribution-id <id> --paths '/*'` to evict edge caches.

### 3.3 Backend auth-middleware rewrite

The new `middleware.CognitoAuth(publicPaths []string)` reads the API GW v2 event from the request context, pulls `sub` from `Authorizer.JWT.Claims`, stuffs it into the context the same way the old middleware does. `GetUserID(ctx)` keeps its signature so every handler that already calls it is unchanged. Local development without API GW (cmd/server with `STORE=memory`) needs a fallback — accept a JWT signed with a dev-only static secret, or pass `X-Dev-User` header. We'll go with the header for simplicity in local dev.

Files deleted in this phase:

- `internal/auth/login/`
- `internal/auth/register/`
- `internal/auth/jwt.go`
- `internal/auth/dynamo_store.go`
- `internal/auth/postgres_store.go` (already gone in Phase 2)
- `internal/auth/store.go`
- `internal/auth/errors.go`
- `internal/auth/` itself (empty after the above)
- `internal/shared/middleware/auth.go` (replaced by cognito_auth.go)
- `internal/shared/middleware/auth_test.go`

Files added:

- `internal/shared/middleware/cognito_auth.go`
- `internal/shared/middleware/cognito_auth_test.go`

Files modified:

- `internal/app/engine.go` — drop `userStore` from deps, replace `middleware.Auth(jwtSecret)` with `middleware.CognitoAuth(publicPaths)`.
- `internal/app/routes.go` — drop `register.RegisterRoutes` + `login.RegisterRoutes` calls.
- `internal/app/store.go` — drop `userStore` from `deps`, drop `auth.NewDynamoUserStore` + `auth.NewMemoryUserStore` paths.

### 3.4 Web app Cognito integration

Three pieces:

- **`@aws-amplify/auth`** as the auth library. Vite + React + Amplify is the path-of-least-resistance for Cognito; `oidc-client-ts` would be smaller but more boilerplate. ~50 KB gzipped.
- **Amplify config** at app boot (`src/main.tsx`): user pool ID, client ID, domain, redirect URLs sourced from env vars (`VITE_COGNITO_USER_POOL_ID`, etc.).
- **Axios interceptor** to attach `Authorization: Bearer <id-token>` on every API request. The ID token is what API GW JWT authorizer expects (not the access token — Cognito issues both; ID token has `sub` + email + cognito:groups, access token has scopes for OAuth flow).

Sign-in flow: hosted UI redirect → callback URL → Amplify parses tokens → main app loads with authenticated state.

### 3.5 CORS on API GW

HTTP API has native CORS config — no Lambda integration needed. The infra `api` module gains a `cors_configuration` block:

```hcl
cors_configuration {
  allow_origins = ["https://<distribution-domain>.cloudfront.net"]
  allow_methods = ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
  allow_headers = ["Authorization", "Content-Type"]
  expose_headers = ["X-Correlation-ID"]
  max_age = 600
}
```

Preflight `OPTIONS` requests get answered by API GW itself, never reaching the Lambda.

---

## 4. Concrete changes per repo

### 4.1 `money-management-infra`

| File | Change |
|---|---|
| `modules/cognito-user-pool/` | New module: user pool + app client + hosted UI domain + outputs. |
| `modules/web-distribution/` | New module: S3 bucket + bucket policy + OAC + CloudFront distribution + ACM cert. |
| `modules/api/main.tf` | Add `cors_configuration` block; add `aws_apigatewayv2_authorizer` for JWT; gate routes on the authorizer. |
| `modules/api/variables.tf` | New `cors_allow_origins`, new `jwt_authorizer` (object with issuer + audiences). |
| `envs/dev/main.tf` | Drop `aws_dynamodb_table.users`, drop `aws_iam_policy.users_table_rw`, drop the role attachment. Drop `random_password.jwt_secret`. Remove `USERS_TABLE` and `JWT_SECRET` env vars from payments Lambda. |
| `envs/dev/auth.tf` (new) | `module.cognito` + its outputs. |
| `envs/dev/web.tf` (new) | `module.web_distribution`. |
| `envs/dev/main.tf` | Wire CORS allow-origin to the CloudFront distribution domain + JWT authorizer to the API. |
| `envs/dev/backend_deploy.tf` | No change (deploy role unchanged). |
| `envs/dev/outputs.tf` | Add `cognito_user_pool_id`, `cognito_client_id`, `cognito_hosted_ui_domain`, `cloudfront_distribution_id`, `cloudfront_distribution_domain`, `web_bucket_name`. |

ACM cert in **us-east-1** requires a separate provider alias:

```hcl
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
```

The `web-distribution` module accepts the aliased provider for the ACM resource.

### 4.2 `backend`

| Path | Change |
|---|---|
| `internal/shared/middleware/cognito_auth.go` | New. Reads claims from the API GW event via the proxy adapter. Local-dev `X-Dev-User` fallback. |
| `internal/shared/middleware/cognito_auth_test.go` | New. Hermetic tests for both paths. |
| `internal/shared/middleware/auth.go` | Delete. |
| `internal/shared/middleware/auth_test.go` | Delete. |
| `internal/auth/` | Delete entire directory. |
| `internal/app/engine.go` | Drop JWT_SECRET, drop userStore. New middleware. |
| `internal/app/store.go` | Drop `userStore` from `deps` struct + DynamoDB / Memory dep builders. |
| `internal/app/routes.go` | Drop `register.RegisterRoutes` + `login.RegisterRoutes` calls. Drop `userStore` arg from signature. |
| `cmd/server/main.go` | No change beyond what `app.BuildEngine` already handles. |
| `cmd/lambda/main.go` | No change. |
| `go.mod` | `golang-jwt/jwt` becomes unused — `go mod tidy` removes it. |
| `docs/aws-migration-phase-4-plan.md` | This document. |

### 4.3 `web`

| Path | Change |
|---|---|
| `package.json` | Add `@aws-amplify/auth`, `aws-amplify`. |
| `src/auth/amplify-config.ts` | New. Reads `VITE_COGNITO_*` env vars, configures Amplify. |
| `src/auth/AuthProvider.tsx` | New. Wraps app, exposes sign-in/sign-out, hosts the redirect callback. |
| `src/main.tsx` | Initialise Amplify, wrap `<AuthProvider>`. |
| `src/api/client.ts` (or wherever axios lives) | New interceptor: fetch current Cognito session, attach `Authorization: Bearer <idToken>`. |
| `src/api/client.ts` | Update base URL to read from `VITE_API_BASE_URL`. |
| `.env.production` | New. Production API URL + Cognito values. |
| `.github/workflows/deploy.yml` | New. Build + sync to S3 + invalidate CloudFront. |
| `vite.config.ts` | Likely no change. |
| `Dockerfile`, `nginx.conf` | Delete — no longer the deployment path. |

---

## 5. Implementation details

### 5.1 Cognito module (sketch)

```hcl
# modules/cognito-user-pool/main.tf
resource "aws_cognito_user_pool" "this" {
  name = var.name

  password_policy {
    minimum_length    = 12
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
    require_uppercase = true
  }

  schema {
    name                = "email"
    attribute_data_type = "String"
    required            = true
    mutable             = true
  }

  auto_verified_attributes = ["email"]
  username_attributes      = ["email"]

  account_recovery_setting {
    recovery_mechanism {
      name     = "verified_email"
      priority = 1
    }
  }
}

resource "aws_cognito_user_pool_client" "this" {
  name         = "${var.name}-spa"
  user_pool_id = aws_cognito_user_pool.this.id

  generate_secret              = false  # SPA = public client
  allowed_oauth_flows          = ["code"]
  allowed_oauth_scopes         = ["openid", "email", "profile"]
  allowed_oauth_flows_user_pool_client = true

  callback_urls = var.callback_urls
  logout_urls   = var.logout_urls

  supported_identity_providers = ["COGNITO"]

  access_token_validity  = 1   # hours
  id_token_validity      = 1
  refresh_token_validity = 30  # days
  token_validity_units {
    access_token  = "hours"
    id_token      = "hours"
    refresh_token = "days"
  }
}

resource "aws_cognito_user_pool_domain" "this" {
  domain       = var.hosted_ui_domain_prefix
  user_pool_id = aws_cognito_user_pool.this.id
}
```

### 5.2 API GW JWT authorizer + CORS

```hcl
# modules/api/main.tf — add:
resource "aws_apigatewayv2_authorizer" "jwt" {
  count = var.jwt_authorizer == null ? 0 : 1

  api_id           = aws_apigatewayv2_api.this.id
  authorizer_type  = "JWT"
  identity_sources = ["$request.header.Authorization"]
  name             = "${var.name}-jwt"

  jwt_configuration {
    issuer   = var.jwt_authorizer.issuer
    audience = var.jwt_authorizer.audiences
  }
}

resource "aws_apigatewayv2_route" "this" {
  for_each = var.routes

  api_id    = aws_apigatewayv2_api.this.id
  route_key = each.key
  target    = "integrations/${aws_apigatewayv2_integration.lambda[each.key].id}"

  authorization_type = var.jwt_authorizer == null ? "NONE" : "JWT"
  authorizer_id      = var.jwt_authorizer == null ? null : aws_apigatewayv2_authorizer.jwt[0].id
}

resource "aws_apigatewayv2_api" "this" {
  name          = var.name
  protocol_type = "HTTP"

  dynamic "cors_configuration" {
    for_each = var.cors_allow_origins == null ? [] : [1]
    content {
      allow_origins = var.cors_allow_origins
      allow_methods = ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
      allow_headers = ["Authorization", "Content-Type"]
      expose_headers = ["X-Correlation-ID"]
      max_age       = 600
    }
  }
}
```

### 5.3 Backend middleware (sketch)

```go
// internal/shared/middleware/cognito_auth.go
package middleware

import (
    "context"
    "net/http"
    "strings"

    "github.com/aws/aws-lambda-go/events"
    "github.com/awslabs/aws-lambda-go-api-proxy/core"
    "github.com/gin-gonic/gin"
)

// CognitoAuth reads validated JWT claims from the API GW v2 event the
// proxy stuffs into the request context. Local-dev (cmd/server) falls
// back to an X-Dev-User header so the gin engine works without an API
// GW in front of it.
func CognitoAuth(publicPaths []string) gin.HandlerFunc {
    return func(c *gin.Context) {
        if isPublic(c.Request.URL.Path, publicPaths) {
            c.Next()
            return
        }

        if userID := userIDFromAPIGW(c.Request.Context()); userID != "" {
            c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), userID))
            c.Next()
            return
        }

        if userID := c.GetHeader("X-Dev-User"); userID != "" {
            c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), userID))
            c.Next()
            return
        }

        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
            "error_code": "unauthorized",
            "message":    "missing or invalid authentication",
        })
    }
}

func userIDFromAPIGW(ctx context.Context) string {
    // aws-lambda-go-api-proxy stashes the event on the context. Read it
    // and pull the sub claim.
    req, ok := core.GetAPIGatewayV2ContextFromContext(ctx)
    if !ok {
        return ""
    }
    if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.JWT == nil {
        return ""
    }
    return req.RequestContext.Authorizer.JWT.Claims["sub"]
}

func isPublic(path string, prefixes []string) bool {
    for _, p := range prefixes {
        if strings.HasPrefix(path, p) {
            return true
        }
    }
    return false
}
```

`GetUserID(ctx)` + `WithUserID(ctx, id)` stay the same — handlers don't change.

Public paths shrink: `/swagger/*` only (no more `/auth/*`).

### 5.4 Web — Amplify auth wiring (sketch)

```ts
// src/auth/amplify-config.ts
import { Amplify } from 'aws-amplify';

export function configureAuth() {
  Amplify.configure({
    Auth: {
      Cognito: {
        userPoolId: import.meta.env.VITE_COGNITO_USER_POOL_ID,
        userPoolClientId: import.meta.env.VITE_COGNITO_CLIENT_ID,
        loginWith: {
          oauth: {
            domain: import.meta.env.VITE_COGNITO_HOSTED_UI_DOMAIN,
            scopes: ['openid', 'email', 'profile'],
            redirectSignIn: [import.meta.env.VITE_AUTH_REDIRECT_URL],
            redirectSignOut: [import.meta.env.VITE_AUTH_REDIRECT_URL],
            responseType: 'code',
          },
        },
      },
    },
  });
}
```

```ts
// src/api/client.ts — axios interceptor
import axios from 'axios';
import { fetchAuthSession } from 'aws-amplify/auth';

const api = axios.create({ baseURL: import.meta.env.VITE_API_BASE_URL });

api.interceptors.request.use(async (config) => {
  const session = await fetchAuthSession();
  const idToken = session.tokens?.idToken?.toString();
  if (idToken) {
    config.headers.Authorization = `Bearer ${idToken}`;
  }
  return config;
});

export default api;
```

### 5.5 Deploy workflow (web)

```yaml
# web/.github/workflows/deploy.yml
name: Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

permissions:
  id-token: write
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
      - run: npm ci
      - name: Build production bundle
        run: npm run build
        env:
          VITE_API_BASE_URL: ${{ vars.VITE_API_BASE_URL }}
          VITE_COGNITO_USER_POOL_ID: ${{ vars.VITE_COGNITO_USER_POOL_ID }}
          VITE_COGNITO_CLIENT_ID: ${{ vars.VITE_COGNITO_CLIENT_ID }}
          VITE_COGNITO_HOSTED_UI_DOMAIN: ${{ vars.VITE_COGNITO_HOSTED_UI_DOMAIN }}
          VITE_AUTH_REDIRECT_URL: ${{ vars.VITE_AUTH_REDIRECT_URL }}
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ vars.AWS_DEPLOY_ROLE_ARN }}
          aws-region: eu-west-1
      - name: Sync to S3
        run: aws s3 sync dist/ s3://${{ vars.WEB_BUCKET_NAME }}/ --delete
      - name: Invalidate CloudFront
        run: |
          aws cloudfront create-invalidation \
            --distribution-id ${{ vars.CLOUDFRONT_DISTRIBUTION_ID }} \
            --paths '/*'
```

A separate `mm-web-deploy-dev` IAM role with `s3:PutObject`/`DeleteObject` on the bucket + `cloudfront:CreateInvalidation` on the distribution. Trust policy mirrors the existing `mm-backend-deploy-dev` pattern — scoped to the `money-management-web` repo's main branch.

---

## 6. Verification

### 6.1 After infra apply

```sh
# Cognito exists
aws cognito-idp list-user-pools --max-results 10 | jq '.UserPools[] | select(.Name == "mm-users-dev")'

# API GW has JWT authorizer
aws apigatewayv2 get-authorizers --api-id $(terraform output -raw api_id) | jq

# CloudFront distribution serves index.html
DIST_DOMAIN=$(terraform output -raw cloudfront_distribution_domain)
curl -sIL "https://$DIST_DOMAIN/" | head -5  # expect 200 OK eventually (15-30 min for CF to propagate)
```

### 6.2 After backend deploy

```sh
# Unauthenticated request gets 401 at API GW (before reaching Lambda)
API=$(terraform output -raw api_endpoint)
curl -sI "$API/wallets"  # expect: 401 Unauthorized

# With a real Cognito token (from the frontend sign-in)
curl -sI "$API/wallets" -H "Authorization: Bearer $COGNITO_ID_TOKEN"  # expect: 200
```

### 6.3 After web deploy

Manually:

1. Visit the CloudFront URL.
2. Hit "Sign in" → redirected to Cognito Hosted UI.
3. Sign up a smoke-test user via the Hosted UI.
4. Confirm via email.
5. Sign in → redirected back, authenticated session.
6. Hit a page that triggers an API call → axios interceptor attaches the token → API responds.

---

## 7. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Existing smoke-test user in `mm-users-dev` is lost when the table drops | High | None | Re-sign up via the Cognito Hosted UI. |
| CORS misconfigured → frontend can't reach the API | High during setup | High | Smoke test (§6) catches it. Iterate. |
| ACM cert in us-east-1 has a separate validation cycle | Med | Low | Use the AWS-provided `*.cloudfront.net` domain for Phase 4 — no custom cert needed yet. |
| Cognito Hosted UI doesn't allow localhost callbacks by default | Med (local dev) | Low | Add `http://localhost:5173` to callback URLs in the user pool client. |
| `aws-amplify/auth` package is heavy (~50 KB gzipped) | Low | Low | Acceptable for an SPA. If it grows, `oidc-client-ts` is the lighter alternative. |
| API GW JWT authorizer caches JWKS aggressively (~1 hr) | Low | Low | Cognito JWKS keys rotate years apart. Not an issue at our scale. |
| Cross-repo deploy ordering fragile — infra must land before backend, backend before web | High | Med | Document the order in this plan §11. Three sequential PRs. |
| `sub` claim format differs between Cognito and the old custom JWT (`sub` is a UUID anyway) | Low | Low | Same UUID format. No aggregate data migration needed. |
| The `X-Dev-User` local-dev fallback gets used in prod by accident | Low | Med | Add a startup check: refuse to start if `MODE=prod` and the header path is enabled. Or: only enable the header path when no API GW context is detectable, AND require an explicit env var `ALLOW_DEV_USER_HEADER=1`. |

---

## 8. Out of scope (explicit)

- 4b — Bedrock for voice. Voice slice stays off in prod.
- 4d — Secrets Manager rotation for AURORA_URL.
- Custom domain on CloudFront (use the default `xxxx.cloudfront.net` URL for now).
- Custom domain on API GW.
- Route53 records.
- Cache-Control / Cache-Tag strategy on the S3 bucket (deferred — currently every deploy invalidates `/*`).
- WAF on CloudFront or API GW.
- Phase 5 observability uplift (dashboards, alarms).

---

## 9. Open questions for review

1. **`@aws-amplify/auth` over `oidc-client-ts`** for the web auth library. Recommend Amplify. Y/N?
2. **Hosted UI** (Cognito-managed sign-in page) over custom React sign-in form. Hosted UI is zero-effort and battle-tested. Y/N?
3. **Cutover-style migration** (drop custom JWT immediately, no dual-auth transition window). Accept that any in-flight tokens get invalidated. For a single-user dev env this is fine. Y/N?
4. **No custom domain in Phase 4** — use the AWS-provided `cloudfront.net` URL for the frontend and `execute-api.amazonaws.com` for the API. Domain + Route53 is a follow-up. Y/N?
5. **`X-Dev-User` header fallback** for local dev (cmd/server with `STORE=memory`) instead of carrying a real Cognito client locally. Y/N?
6. **No password policy strength beyond Cognito's defaults** for dev. Y/N?
7. **Drop `mm-users-dev` DDB table in this PR** rather than leaving it as a dormant resource. Y/N?
8. **Cognito user pool config: email as username, self-sign-up enabled, email verification required** (matches the existing custom flow). Y/N?
9. **CORS allow_origins set to the CloudFront domain only** — no wildcard, no localhost. Local frontend dev still works because Vite dev server runs same-origin via the API proxy or by hitting the dev API directly (`/auth/*` paths are gone, so no auth issue locally). Add localhost later if needed. Y/N?
10. **Three sequential PRs in strict order** (infra → backend → web), each pushed and applied before the next. Y/N?

---

## 10. Execution order

Strict order matters because each step's contract depends on the previous step's resources existing.

1. **Infra PR — Cognito + S3 + CloudFront + CORS + JWT authorizer wired to API.** Apply. Capture outputs.
2. **Backend PR — Drop auth slice, swap middleware to CognitoAuth.** Push to main, CI deploys.
3. **Web PR — Add Cognito sign-in, deploy workflow.** Push to main, CI deploys to S3.

Between (1) and (2): the deployed API will reject every request with 401 until the frontend can produce Cognito tokens. That's fine for a dev env — no real users hitting it.

Between (2) and (3): same — no frontend yet.

After (3): everything works end-to-end.

---

## 11. Glossary additions

- **Hosted UI** — Cognito-managed sign-in / sign-up pages. Configurable colours; no React needed.
- **Origin Access Control (OAC)** — Newer CloudFront → S3 auth pattern. Replaces the older Origin Access Identity (OAI). Required for fine-grained bucket policies.
- **JWKS** — JSON Web Key Set. The public keys Cognito publishes so JWT verifiers (like API GW) can validate signatures.

---

**End of plan. Mark up directly with comments, or send replies via the questions in §9.**
