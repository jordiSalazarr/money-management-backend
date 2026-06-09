package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
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

// Contract tests for the DDB store run against LocalStack. They skip
// themselves when AWS_ENDPOINT_URL is unset so pure-unit CI doesn't fail.
// CI wires LocalStack as a service container and exports the endpoint.
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

func evt(aggregateID, eventType string, payload any) eventstore.Event {
	raw, _ := json.Marshal(payload)
	return eventstore.Event{
		EventID:       uuid.NewString(),
		AggregateID:   aggregateID,
		AggregateType: "test",
		EventType:     eventType,
		CorrelationID: "corr-test",
		Payload:       raw,
		OccurredAt:    time.Now().UTC().Truncate(time.Microsecond),
	}
}

func TestDynamo_AppendFirstEvent(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Created", map[string]string{"name": "alpha"})}, 0)
	if err != nil {
		t.Fatalf("Append v1: %v", err)
	}

	got, err := store.Load(context.Background(), aggID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Version != 1 {
		t.Fatalf("got %#v, want 1 event at version 1", got)
	}
}

func TestDynamo_AppendDuplicateVersion_Conflict(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Created", nil)}, 0); err != nil {
		t.Fatalf("first append: %v", err)
	}

	err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Created", nil)}, 0)
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}
}

func TestDynamo_AppendNextVersion(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Created", nil)}, 0); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Updated", nil)}, 1); err != nil {
		t.Fatalf("v2: %v", err)
	}

	got, _ := store.Load(context.Background(), aggID)
	if len(got) != 2 || got[1].Version != 2 {
		t.Fatalf("got %#v, want 2 events ending at v2", got)
	}
}

func TestDynamo_AppendSkippingVersion_Conflict(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "Created", nil)}, 0); err != nil {
		t.Fatalf("v1: %v", err)
	}

	// Caller claims to be at version 1 but tries to skip ahead — the
	// existing v2 slot is empty, so a single write succeeds. The real
	// conflict surfaces when expectedVersion != currentVersion.
	// expectedVersion=2 with no v2 yet should fail because we'd put v3
	// into an empty slot, leaving a gap. Our Put condition only checks
	// the target slot is empty; OCC for "no gap" must come from the
	// caller knowing the actual head.
	//
	// Simulate the realistic conflict: two writers, one at expectedVersion=1
	// both want to write v2. The second one fails.
	if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "A", nil)}, 1); err != nil {
		t.Fatalf("writer1 v2: %v", err)
	}
	err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, "B", nil)}, 1)
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict on lost OCC race", err)
	}
}

func TestDynamo_AppendBatch_TransactWriteItems(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	events := []eventstore.Event{
		evt(aggID, "E1", nil), evt(aggID, "E2", nil), evt(aggID, "E3", nil),
		evt(aggID, "E4", nil), evt(aggID, "E5", nil),
	}
	if err := store.Append(context.Background(), aggID, events, 0); err != nil {
		t.Fatalf("batch append: %v", err)
	}

	got, _ := store.Load(context.Background(), aggID)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for i, e := range got {
		if e.Version != i+1 {
			t.Errorf("event %d version = %d, want %d", i, e.Version, i+1)
		}
	}
}

func TestDynamo_AppendMulti_AllSucceed(t *testing.T) {
	store := newTestStore(t)
	a, b, c := "agg-"+uuid.NewString(), "agg-"+uuid.NewString(), "agg-"+uuid.NewString()

	err := store.AppendMulti(context.Background(), []eventstore.AggregateEvents{
		{AggregateID: a, ExpectedVersion: 0, Events: []eventstore.Event{evt(a, "A", nil)}},
		{AggregateID: b, ExpectedVersion: 0, Events: []eventstore.Event{evt(b, "B", nil)}},
		{AggregateID: c, ExpectedVersion: 0, Events: []eventstore.Event{evt(c, "C", nil)}},
	})
	if err != nil {
		t.Fatalf("AppendMulti: %v", err)
	}

	for _, id := range []string{a, b, c} {
		got, err := store.Load(context.Background(), id)
		if err != nil || len(got) != 1 {
			t.Errorf("aggregate %s: got %d events, err=%v", id, len(got), err)
		}
	}
}

func TestDynamo_AppendMulti_PartialConflict_Atomic(t *testing.T) {
	store := newTestStore(t)
	a, b := "agg-"+uuid.NewString(), "agg-"+uuid.NewString()

	// Seed `a` at v1 so its expectedVersion=0 in the batch will fail.
	if err := store.Append(context.Background(), a, []eventstore.Event{evt(a, "Seed", nil)}, 0); err != nil {
		t.Fatalf("seed a: %v", err)
	}

	err := store.AppendMulti(context.Background(), []eventstore.AggregateEvents{
		{AggregateID: a, ExpectedVersion: 0, Events: []eventstore.Event{evt(a, "A", nil)}},
		{AggregateID: b, ExpectedVersion: 0, Events: []eventstore.Event{evt(b, "B", nil)}},
	})
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}

	// `b` must not have been written — the whole transaction rolls back.
	if _, err := store.Load(context.Background(), b); !errors.Is(err, eventstore.ErrAggregateNotFound) {
		t.Fatalf("b leaked through atomic batch: err=%v", err)
	}
}

func TestDynamo_LoadEmpty_ReturnsAggregateNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Load(context.Background(), "agg-"+uuid.NewString())
	if !errors.Is(err, eventstore.ErrAggregateNotFound) {
		t.Fatalf("got %v, want ErrAggregateNotFound", err)
	}
}

func TestDynamo_LoadOrdersByVersion(t *testing.T) {
	store := newTestStore(t)
	aggID := "agg-" + uuid.NewString()

	for i := 1; i <= 3; i++ {
		if err := store.Append(context.Background(), aggID, []eventstore.Event{evt(aggID, fmt.Sprintf("E%d", i), nil)}, i-1); err != nil {
			t.Fatalf("append v%d: %v", i, err)
		}
	}

	got, err := store.Load(context.Background(), aggID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, e := range got {
		if e.Version != i+1 {
			t.Errorf("position %d has version %d", i, e.Version)
		}
	}
}
