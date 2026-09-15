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

// DynamoStore is a DynamoDB-backed event store. It satisfies Store but not
// Feed: read models live behind a separate projector Lambda consuming the
// table's Kinesis stream, so there is no global log to subscribe to here.
//
// It is scheduled for deletion in phase 4.5 along with cmd/lambda. It is kept
// compiling against the split port in the meantime so the tree stays green
// while phases 1-3 land.
//
// Note the sort key is now the 0-based stream revision rather than the old
// 1-based version count, matching KurrentDB's ExpectedRevision. Existing table
// data is not migrated — the plan starts from an empty store either way.
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string
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
	return &DynamoStore{client: client, tableName: tableName}
}

func (s *DynamoStore) Append(ctx context.Context, stream StreamID, events []Event, expectedVersion int) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) == 1 {
		item := s.itemFor(stream, expectedVersion+1, events[0])
		_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String(s.tableName),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(version)"),
		})
		return mapDDBErr(err)
	}
	return s.appendBatch(ctx, stream, events, expectedVersion)
}

func (s *DynamoStore) appendBatch(ctx context.Context, stream StreamID, events []Event, expectedVersion int) error {
	items := make([]types.TransactWriteItem, len(events))
	for i := range events {
		items[i] = types.TransactWriteItem{
			Put: &types.Put{
				TableName:           aws.String(s.tableName),
				Item:                s.itemFor(stream, expectedVersion+1+i, events[i]),
				ConditionExpression: aws.String("attribute_not_exists(version)"),
			},
		}
	}
	_, err := s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	return mapDDBErr(err)
}

func (s *DynamoStore) Load(ctx context.Context, stream StreamID) ([]Recorded, error) {
	var recorded []Recorded
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.tableName),
			KeyConditionExpression: aws.String("aggregate_id = :id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":id": &types.AttributeValueMemberS{Value: stream.String()},
			},
			ConsistentRead:    aws.Bool(true),
			ScanIndexForward:  aws.Bool(true),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("querying events: %w", err)
		}
		for _, item := range out.Items {
			r, err := recordedFromItem(stream, item)
			if err != nil {
				return nil, err
			}
			recorded = append(recorded, r)
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	if len(recorded) == 0 {
		return nil, ErrAggregateNotFound
	}
	return recorded, nil
}

func (s *DynamoStore) itemFor(stream StreamID, revision int, e Event) map[string]types.AttributeValue {
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("null")
	}
	return map[string]types.AttributeValue{
		"aggregate_id":   &types.AttributeValueMemberS{Value: stream.String()},
		"version":        &types.AttributeValueMemberN{Value: strconv.Itoa(revision)},
		"event_id":       &types.AttributeValueMemberS{Value: e.EventID},
		"aggregate_type": &types.AttributeValueMemberS{Value: stream.Category},
		"event_type":     &types.AttributeValueMemberS{Value: e.EventType},
		"schema_version": &types.AttributeValueMemberN{Value: strconv.Itoa(e.SchemaVersion)},
		"correlation_id": &types.AttributeValueMemberS{Value: e.CorrelationID},
		"payload":        &types.AttributeValueMemberS{Value: string(e.Payload)},
		"occurred_at":    &types.AttributeValueMemberS{Value: e.OccurredAt.UTC().Format(time.RFC3339Nano)},
		"recorded_at":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
	}
}

func recordedFromItem(stream StreamID, item map[string]types.AttributeValue) (Recorded, error) {
	r := Recorded{Stream: stream}
	if v, ok := item["version"].(*types.AttributeValueMemberN); ok {
		n, err := strconv.Atoi(v.Value)
		if err != nil {
			return r, fmt.Errorf("parsing version: %w", err)
		}
		r.Revision = n
	}
	if v, ok := item["schema_version"].(*types.AttributeValueMemberN); ok {
		n, err := strconv.Atoi(v.Value)
		if err != nil {
			return r, fmt.Errorf("parsing schema_version: %w", err)
		}
		r.SchemaVersion = n
	}
	if v, ok := item["event_id"].(*types.AttributeValueMemberS); ok {
		r.EventID = v.Value
	}
	if v, ok := item["event_type"].(*types.AttributeValueMemberS); ok {
		r.EventType = v.Value
	}
	if v, ok := item["correlation_id"].(*types.AttributeValueMemberS); ok {
		r.CorrelationID = v.Value
	}
	if v, ok := item["payload"].(*types.AttributeValueMemberS); ok {
		r.Payload = json.RawMessage(v.Value)
	}
	if v, ok := item["occurred_at"].(*types.AttributeValueMemberS); ok {
		t, err := time.Parse(time.RFC3339Nano, v.Value)
		if err != nil {
			return r, fmt.Errorf("parsing occurred_at: %w", err)
		}
		r.OccurredAt = t
	}
	return r, nil
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

var _ Store = (*DynamoStore)(nil)
