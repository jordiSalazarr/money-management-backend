// cmd/lambda is the API Gateway HTTP API → Lambda entry point.
//
// Init-time wiring (build the gin engine once per cold start) amortises
// cost across warm invocations. The same gin engine that cmd/server runs
// is wrapped in an APIGatewayV2 adapter, so middleware and slice handlers
// see a normal *http.Request and behave identically to the HTTP server.
//
// Failure to construct the engine in init() panics the function — every
// invocation wiºll 500 until a fixed binary redeploys. That's intentional:
// silent degradation (e.g. running without a Dynamo client) would be
// strictly worse.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
)

var ginLambda *ginadapter.GinLambdaV2

// func init() {
// 	// Skip wiring during `go test` / `go vet` — init() runs before tests
// 	// too, and we don't want a test process to try to reach DynamoDB.
// 	if os.Getenv("SKIP_LAMBDA_INIT") == "1" {
// 		return
// 	}

// 	logr := logger.New()
// 	// MountQueryRoutes is requested unconditionally; BuildEngine silently
// 	// downgrades it to false when no projection stores were built (the
// 	// AURORA_URL-unset case during the phase 1→2 transition).
// 	engine, _, err := app.BuildEngine(context.Background(), logr, app.Options{
// 		MountQueryRoutes: true,
// 		DefaultStore:     app.StoreModeDynamoDB,
// 	})
// 	if err != nil {
// 		log.Fatalf("build engine: %v", err)
// 	}
// 	ginLambda = ginadapter.NewV2(engine)
// }

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	// lambda.Start(handler)
	res := reverseVowels("leetcode")
	fmt.Printf("result is: %s", res)
}

// Given a string s, reverse only all the vowels in the string and return it.

// The vowels are 'a', 'e', 'i', 'o', and 'u', and they can appear in both lower and upper cases, more than once.

// Example 1:

// Input: s = "IceCreAm"

// Output: "AceCreIm"

// Explanation:

// The vowels in s are ['I', 'e', 'e', 'A']. On reversing the vowels, s becomes "AceCreIm".

// Example 2:

// Input: s = "leetcode"

// Output: "leotcede"
var vowels = map[string]bool{
	"a": true,
	"e": true,
	"i": true,
	"o": true,
	"u": true,
	"A": true,
	"E": true,
	"I": true,
	"O": true,
	"U": true,
}

func isVowel(b string) bool {
	_, found := vowels[(b)]
	return found

}

func reverseVowels(s string) string {
	arr := strings.Split(s, "")
	left := 0
	right := len(s) - 1
	for left < right {
		leftIsVowel := isVowel(arr[left])
		rightIsVowel := isVowel(arr[right])
		if !rightIsVowel && !leftIsVowel {
			left++
			right--
			continue
		}

		if leftIsVowel && rightIsVowel {
			leftVal := arr[left]
			arr[left] = arr[right]
			arr[right] = leftVal
			left++
			right--
			continue
		}
		if leftIsVowel {
			right--
		}
		if rightIsVowel {
			left++
		}
	}

	return strings.Join(arr, "")

}
