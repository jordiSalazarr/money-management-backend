package eventstore_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// The DDB store runs the same write-side contract as every other store. It
// does not implement Feed — projections come off the table's Kinesis stream,
// not a global log — so RunFeedContract does not apply to it.
//
// These skip themselves when AWS_ENDPOINT_URL is unset so pure-unit CI doesn't
// fail; CI wires LocalStack as a service container and exports the endpoint.
func TestDynamoStoreSatisfiesTheContract(t *testing.T) {
	RunStoreContract(t, "dynamodb", func(t *testing.T) eventstore.Store {
		return newTestStore(t)
	})
}

func newTestStore(t *testing.T) *eventstore.DynamoStore {
	t.Helper()
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	if endpoint == "" {
		t.Skip("AWS_ENDPOINT_URL not set — skipping DDB contract tests")
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("eu-west-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("loading AWS config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	tableName := fmt.Sprintf("mm-events-test-%s", uuid.NewString())
	createTestTable(t, client, tableName)
	t.Cleanup(func() { deleteTestTable(t, client, tableName) })

	return eventstore.NewDynamoStoreFromClient(client, tableName)
}

func createTestTable(t *testing.T, client *dynamodb.Client, name string) {
	t.Helper()
	ctx := context.Background()
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(name),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("aggregate_id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("version"), AttributeType: types.ScalarAttributeTypeN},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("aggregate_id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("version"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatalf("creating test table %s: %v", name, err)
	}
	waiter := dynamodb.NewTableExistsWaiter(client)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)}, 30*time.Second); err != nil {
		t.Fatalf("waiting for test table %s: %v", name, err)
	}
}

func deleteTestTable(t *testing.T, client *dynamodb.Client, name string) {
	t.Helper()
	_, err := client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	if err != nil {
		t.Logf("cleanup: deleting %s: %v", name, err)
	}
}
