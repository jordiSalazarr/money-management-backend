package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoStore is a DynamoDB-backed event store.
//
// The store does not fan out to in-process subscribers. Read models live
// behind a separate projector Lambda that consumes the table's Kinesis
// stream (Phase 2). Subscribe and ReplayAll are therefore no-ops; the
// HTTP entry point doesn't call them in Lambda mode either.
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string

	// registry is kept for interface compatibility with the in-memory and
	// Postgres stores. The DDB store doesn't notify, so it never consults
	// the registry today — but cmd/server still calls RegisterEvent at
	// startup unconditionally, and we want that to be a safe no-op.
	registry map[string]func() any
}

// NewDynamoStore builds a DynamoStore using the default AWS config chain.
//
// LocalStack: set AWS_ENDPOINT_URL=http://localhost:4566 (SDK v2 picks it
// up automatically — no custom resolver needed).
func NewDynamoStore(ctx context.Context, tableName string) (*DynamoStore, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return NewDynamoStoreFromClient(dynamodb.NewFromConfig(cfg), tableName), nil
}

// NewDynamoStoreFromClient lets tests inject a pre-built client that
// points at LocalStack. The constructor itself doesn't make network calls.
func NewDynamoStoreFromClient(client *dynamodb.Client, tableName string) *DynamoStore {
	return &DynamoStore{
		client:    client,
		tableName: tableName,
		registry:  make(map[string]func() any),
	}
}

func (s *DynamoStore) RegisterEvent(eventType string, factory func() any) {
	s.registry[eventType] = factory
}

// Subscribe is a no-op. The DDB store relies on the Kinesis stream + a
// dedicated projector Lambda; in-process projections aren't wired here.
func (s *DynamoStore) Subscribe(_ Subscriber) {}

func (s *DynamoStore) Append(ctx context.Context, aggregateID string, events []Event, expectedVersion int) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) == 1 {
		events[0].Version = expectedVersion + 1
		item := s.itemFor(aggregateID, events[0])
		_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String(s.tableName),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(version)"),
		})
		return mapDDBErr(err)
	}
	return s.appendBatch(ctx, aggregateID, events, expectedVersion)
}

func (s *DynamoStore) AppendMulti(ctx context.Context, batches []AggregateEvents) error {
	var items []types.TransactWriteItem
	for _, batch := range batches {
		for i := range batch.Events {
			batch.Events[i].Version = batch.ExpectedVersion + i + 1
			items = append(items, types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.tableName),
					Item:                s.itemFor(batch.AggregateID, batch.Events[i]),
					ConditionExpression: aws.String("attribute_not_exists(version)"),
				},
			})
		}
	}
	if len(items) == 0 {
		return nil
	}
	_, err := s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	return mapDDBErr(err)
}

func (s *DynamoStore) appendBatch(ctx context.Context, aggregateID string, events []Event, expectedVersion int) error {
	items := make([]types.TransactWriteItem, len(events))
	for i := range events {
		events[i].Version = expectedVersion + i + 1
		items[i] = types.TransactWriteItem{
			Put: &types.Put{
				TableName:           aws.String(s.tableName),
				Item:                s.itemFor(aggregateID, events[i]),
				ConditionExpression: aws.String("attribute_not_exists(version)"),
			},
		}
	}
	_, err := s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	return mapDDBErr(err)
}

func (s *DynamoStore) Load(ctx context.Context, aggregateID string) ([]Event, error) {
	var events []Event
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.tableName),
			KeyConditionExpression: aws.String("aggregate_id = :id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":id": &types.AttributeValueMemberS{Value: aggregateID},
			},
			ConsistentRead:    aws.Bool(true),
			ScanIndexForward:  aws.Bool(true),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("querying events: %w", err)
		}
		for _, item := range out.Items {
			e, err := eventFromItem(item)
			if err != nil {
				return nil, err
			}
			events = append(events, e)
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	if len(events) == 0 {
		return nil, ErrAggregateNotFound
	}
	return events, nil
}

func (s *DynamoStore) LoadAll(ctx context.Context) ([]Event, error) {
	var events []Event
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scanning events: %w", err)
		}
		for _, item := range out.Items {
			e, err := eventFromItem(item)
			if err != nil {
				return nil, err
			}
			events = append(events, e)
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return events, nil
}

// ReplayAll is a no-op for the DDB store. The projector Lambda owns
// projection state out-of-process — there are no in-process subscribers
// to fan out to.
func (s *DynamoStore) ReplayAll(_ context.Context) error { return nil }

func (s *DynamoStore) itemFor(aggregateID string, e Event) map[string]types.AttributeValue {
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("null")
	}
	return map[string]types.AttributeValue{
		"aggregate_id":   &types.AttributeValueMemberS{Value: aggregateID},
		"version":        &types.AttributeValueMemberN{Value: strconv.Itoa(e.Version)},
		"event_id":       &types.AttributeValueMemberS{Value: e.EventID},
		"aggregate_type": &types.AttributeValueMemberS{Value: e.AggregateType},
		"event_type":     &types.AttributeValueMemberS{Value: e.EventType},
		"correlation_id": &types.AttributeValueMemberS{Value: e.CorrelationID},
		"payload":        &types.AttributeValueMemberS{Value: string(e.Payload)},
		"occurred_at":    &types.AttributeValueMemberS{Value: e.OccurredAt.UTC().Format(time.RFC3339Nano)},
		"recorded_at":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
	}
}

func eventFromItem(item map[string]types.AttributeValue) (Event, error) {
	e := Event{}
	if v, ok := item["aggregate_id"].(*types.AttributeValueMemberS); ok {
		e.AggregateID = v.Value
	}
	if v, ok := item["version"].(*types.AttributeValueMemberN); ok {
		n, err := strconv.Atoi(v.Value)
		if err != nil {
			return e, fmt.Errorf("parsing version: %w", err)
		}
		e.Version = n
	}
	if v, ok := item["event_id"].(*types.AttributeValueMemberS); ok {
		e.EventID = v.Value
	}
	if v, ok := item["aggregate_type"].(*types.AttributeValueMemberS); ok {
		e.AggregateType = v.Value
	}
	if v, ok := item["event_type"].(*types.AttributeValueMemberS); ok {
		e.EventType = v.Value
	}
	if v, ok := item["correlation_id"].(*types.AttributeValueMemberS); ok {
		e.CorrelationID = v.Value
	}
	if v, ok := item["payload"].(*types.AttributeValueMemberS); ok {
		e.Payload = json.RawMessage(v.Value)
	}
	if v, ok := item["occurred_at"].(*types.AttributeValueMemberS); ok {
		t, err := time.Parse(time.RFC3339Nano, v.Value)
		if err != nil {
			return e, fmt.Errorf("parsing occurred_at: %w", err)
		}
		e.OccurredAt = t
	}
	return e, nil
}

// mapDDBErr collapses DynamoDB's two flavours of OCC failure into the
// shared ErrConcurrencyConflict so callers don't need to switch on the
// underlying SDK error type.
func mapDDBErr(err error) error {
	if err == nil {
		return nil
	}
	var ccfe *types.ConditionalCheckFailedException
	if errors.As(err, &ccfe) {
		return ErrConcurrencyConflict
	}
	var tce *types.TransactionCanceledException
	if errors.As(err, &tce) {
		for _, r := range tce.CancellationReasons {
			if aws.ToString(r.Code) == "ConditionalCheckFailed" {
				return ErrConcurrencyConflict
			}
		}
	}
	return fmt.Errorf("dynamodb: %w", err)
}
