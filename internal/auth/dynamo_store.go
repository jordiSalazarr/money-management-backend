package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoUserStore is a thin DDB-backed UserStorer used by the Lambda
// entry point. It deliberately stays minimal — Phase 4 swaps everything
// here for Cognito + the API Gateway JWT authorizer, so investing in
// migrations, secondary indexes, or richer queries would be wasted work.
//
// The table is keyed on `email` (PK only). Required attributes match the
// User struct exactly:
//
//	email          S (PK)
//	id             S
//	password_hash  S
//	created_at     S (RFC3339Nano)
type DynamoUserStore struct {
	client    *dynamodb.Client
	tableName string
}

func NewDynamoUserStore(ctx context.Context, tableName string) (*DynamoUserStore, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return NewDynamoUserStoreFromClient(dynamodb.NewFromConfig(cfg), tableName), nil
}

func NewDynamoUserStoreFromClient(client *dynamodb.Client, tableName string) *DynamoUserStore {
	return &DynamoUserStore{client: client, tableName: tableName}
}

func (s *DynamoUserStore) Create(user *User) error {
	ctx := context.Background()
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"email":         &types.AttributeValueMemberS{Value: user.Email},
			"id":            &types.AttributeValueMemberS{Value: user.ID},
			"password_hash": &types.AttributeValueMemberS{Value: user.PasswordHash},
			"created_at":    &types.AttributeValueMemberS{Value: user.CreatedAt.UTC().Format(time.RFC3339Nano)},
		},
		ConditionExpression: aws.String("attribute_not_exists(email)"),
	})
	if err != nil {
		var ccfe *types.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return ErrEmailTaken
		}
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

func (s *DynamoUserStore) FindByEmail(email string) (*User, error) {
	ctx := context.Background()
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"email": &types.AttributeValueMemberS{Value: email},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrUserNotFound
	}

	user := &User{}
	if v, ok := out.Item["email"].(*types.AttributeValueMemberS); ok {
		user.Email = v.Value
	}
	if v, ok := out.Item["id"].(*types.AttributeValueMemberS); ok {
		user.ID = v.Value
	}
	if v, ok := out.Item["password_hash"].(*types.AttributeValueMemberS); ok {
		user.PasswordHash = v.Value
	}
	if v, ok := out.Item["created_at"].(*types.AttributeValueMemberS); ok {
		t, err := time.Parse(time.RFC3339Nano, v.Value)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		user.CreatedAt = t
	}
	return user, nil
}
