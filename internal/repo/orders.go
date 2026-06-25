package repo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"komodo-order-api/internal/config"
	"komodo-order-api/internal/models"

	ddbTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	dyn "github.com/rdevitto86/komodo-forge-sdk-go/aws/dynamodb"
)

var DynDB *dyn.Client

// table is resolved at startup from the env (populated by secrets-manager bootstrap).
var table = os.Getenv(config.DYNAMODB_ORDERS_TABLE)

// GSI names match the table definition in infra/local/localstack/init/scripts/init-dynamodb.sh
// and infra/deploy/cfn/infra.yaml.
const (
	gsiUserIndex  = "user-index"
	gsiEmailIndex = "email-index"
)

// orderRecord is the DynamoDB representation of an order.
// Table schema: hash-only PK (no SK). See docs/data-model.md for full design.
//
// PK = DisplayID (e.g. "KFS-2504-7XK3M9")
// GSI user-index:  PK=userId, SK=createdAt  — list all orders for a user, newest first
// GSI email-index: PK=email,  SK=createdAt  — guest order lookup by email
//
// userId carries the owner-key prefix: "USER#<id>" for registered accounts,
// "GUEST#<uuid>" for unauthenticated placements. See openapi.yaml Order.userId.
type orderRecord struct {
	PK        string `dynamodbav:"PK"`
	ID        string `dynamodbav:"id"`
	Seq       int64  `dynamodbav:"seq"`
	Type      string `dynamodbav:"type"`
	UserID    string `dynamodbav:"userId"`
	Email     string `dynamodbav:"email,omitempty"`
	Status    string `dynamodbav:"status"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`

	Items   []models.OrderItem  `dynamodbav:"lineItems"`
	Address models.OrderAddress `dynamodbav:"address"`
	Payment models.OrderPayment `dynamodbav:"payment"`
	Totals  models.OrderTotals  `dynamodbav:"totals"`
}

// counterRecord is the DynamoDB shape of a COUNTER#* item after an ADD increment.
type counterRecord struct {
	Seq int64 `dynamodbav:"seq"`
}

// IncrementOrderSeq atomically increments COUNTER#ORDER and returns the new sequence value.
// The COUNTER#ORDER item must be seeded at seq=0 before the first order is placed
// (done by the LocalStack init script and the CloudFormation template).
func IncrementOrderSeq(ctx context.Context) (int64, error) {
	key := map[string]ddbTypes.AttributeValue{
		"PK": &ddbTypes.AttributeValueMemberS{Value: "COUNTER#ORDER"},
	}
	var result counterRecord
	if err := DynDB.UpdateItemAs(ctx, table, key,
		"ADD seq :one",
		map[string]ddbTypes.AttributeValue{
			":one": &ddbTypes.AttributeValueMemberN{Value: "1"},
		},
		nil, nil, &result,
	); err != nil {
		return 0, fmt.Errorf("repo.IncrementOrderSeq: %w", err)
	}
	return result.Seq, nil
}

// CreateOrder writes a new order to DynamoDB with a condition preventing overwrites.
// order.DisplayID is used as the table PK. The caller (service layer) is responsible
// for generating a unique, correctly-formatted DisplayID before calling this function.
// order.UserID must already carry the owner-key prefix ("USER#<id>" or "GUEST#<uuid>").
func CreateOrder(ctx context.Context, order *models.Order) error {
	rec := orderRecord{
		PK:        order.DisplayID,
		ID:        order.ID,
		Seq:       order.Seq,
		Type:      "ORDER",
		UserID:    order.UserID,
		Email:     order.Email,
		Status:    string(order.Status),
		CreatedAt: order.CreatedAt,
		UpdatedAt: order.UpdatedAt,
		Items:     order.Items,
		Address:   order.Address,
		Payment:   order.Payment,
		Totals:    order.Totals,
	}

	condition := "attribute_not_exists(PK)"
	if err := DynDB.WriteItemFrom(ctx, table, rec, false, nil, &condition); err != nil {
		return fmt.Errorf("repo.CreateOrder: put: %w", err)
	}
	return nil
}

// GetOrder fetches an order by its DisplayID (the table PK).
// Returns models.ErrNotFound (wrapped) when the item does not exist.
func GetOrder(ctx context.Context, orderID string) (*models.Order, error) {
	key := map[string]ddbTypes.AttributeValue{
		"PK": &ddbTypes.AttributeValueMemberS{Value: orderID},
	}

	var rec orderRecord
	if err := DynDB.GetItemAs(ctx, table, key, false, nil, &rec); err != nil {
		return nil, fmt.Errorf("repo.GetOrder: %w", models.ErrNotFound)
	}
	if rec.PK == "" {
		return nil, fmt.Errorf("repo.GetOrder: empty record: %w", models.ErrNotFound)
	}

	return recordToModel(&rec), nil
}

// ListOrdersByUser returns a page of orders for a user via the user-index GSI
// (userId = ownerKey), sorted newest-first (descending by createdAt).
//
// limit controls the page size (0 defaults to 20; max is capped at 100).
// cursor is an opaque continuation token from a previous response (empty = first page).
// Returns the orders, a next-page cursor (empty string if no more pages), and any error.
func ListOrdersByUser(ctx context.Context, userID string, limit int, cursor string) ([]*models.Order, string, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	lim := int32(limit)
	scanForward := false
	input := dyn.QueryInput{
		TableName:              table,
		IndexName:              &[]string{gsiUserIndex}[0],
		KeyConditionExpression: "userId = :uid",
		ExpressionValues: map[string]ddbTypes.AttributeValue{
			":uid": &ddbTypes.AttributeValueMemberS{Value: userID},
		},
		ScanIndexForward: &scanForward,
		Limit:            &lim,
	}

	if cursor != "" {
		startKey, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("repo.ListOrdersByUser: decode cursor: %w", err)
		}
		input.ExclusiveStartKey = startKey
	}

	var records []orderRecord
	out, err := DynDB.QueryAs(ctx, input, &records)
	if err != nil {
		return nil, "", fmt.Errorf("repo.ListOrdersByUser: query: %w", err)
	}

	orders := make([]*models.Order, 0, len(records))
	for i := range records {
		orders = append(orders, recordToModel(&records[i]))
	}

	nextCursor := ""
	if out != nil && len(out.LastEvaluatedKey) > 0 {
		nextCursor, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("repo.ListOrdersByUser: encode cursor: %w", err)
		}
	}

	return orders, nextCursor, nil
}

// UpdateOrderStatus conditionally transitions the order to newStatus.
// The write is rejected if the stored status != expectedStatus, guarding against
// concurrent writes racing on the same order.
//
// "status" is a DynamoDB reserved word — #s is used as an expression attribute name.
func UpdateOrderStatus(ctx context.Context, orderID string, newStatus models.OrderStatus, expectedStatus models.OrderStatus) error {
	key := map[string]ddbTypes.AttributeValue{
		"PK": &ddbTypes.AttributeValueMemberS{Value: orderID},
	}

	condition := "attribute_exists(PK) AND #s = :expected"
	if err := DynDB.UpdateItemAs(
		ctx,
		table,
		key,
		"SET #s = :new, updatedAt = :ua",
		map[string]ddbTypes.AttributeValue{
			":new":      &ddbTypes.AttributeValueMemberS{Value: string(newStatus)},
			":expected": &ddbTypes.AttributeValueMemberS{Value: string(expectedStatus)},
			":ua":       &ddbTypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
		map[string]string{
			"#s": "status",
		},
		&condition,
		nil,
	); err != nil {
		// The forge SDK wraps ConditionalCheckFailedException — inspect the message
		// to distinguish a condition failure from a connectivity error.
		// TODO: once the SDK exposes a typed sentinel for condition failures, use errors.As.
		if isConditionFailure(err) {
			return fmt.Errorf("repo.UpdateOrderStatus: condition failed: %w", models.ErrInvalidTransition)
		}
		return fmt.Errorf("repo.UpdateOrderStatus: update: %w", err)
	}
	return nil
}

// recordToModel converts a DynamoDB orderRecord to the Order domain model.
func recordToModel(rec *orderRecord) *models.Order {
	return &models.Order{
		ID:        rec.ID,
		DisplayID: rec.PK,
		Seq:       rec.Seq,
		UserID:    rec.UserID,
		Email:     rec.Email,
		Status:    models.OrderStatus(rec.Status),
		Items:     rec.Items,
		Address:   rec.Address,
		Payment:   rec.Payment,
		Totals:    rec.Totals,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}

// isConditionFailure reports whether err originated from a DynamoDB
// ConditionalCheckFailedException. The forge SDK wraps the error without
// exporting a typed sentinel; string matching is a stopgap until it does.
func isConditionFailure(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "ConditionalCheckFailedException")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// encodeCursor serialises a DynamoDB LastEvaluatedKey to a URL-safe base64 JSON string.
// The user-index pagination key includes PK (hash-only table) + GSI keys (userId, createdAt).
func encodeCursor(key map[string]ddbTypes.AttributeValue) (string, error) {
	simple := make(map[string]string, len(key))
	for k, v := range key {
		sv, ok := v.(*ddbTypes.AttributeValueMemberS)
		if !ok {
			return "", fmt.Errorf("encodeCursor: unexpected non-string attribute %q in pagination key", k)
		}
		simple[k] = sv.Value
	}
	b, err := json.Marshal(simple)
	if err != nil {
		return "", fmt.Errorf("encodeCursor: marshal: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor reverses encodeCursor.
func decodeCursor(cursor string) (map[string]ddbTypes.AttributeValue, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("decodeCursor: base64: %w", err)
	}
	var simple map[string]string
	if err := json.Unmarshal(b, &simple); err != nil {
		return nil, fmt.Errorf("decodeCursor: unmarshal: %w", err)
	}
	result := make(map[string]ddbTypes.AttributeValue, len(simple))
	for k, v := range simple {
		result[k] = &ddbTypes.AttributeValueMemberS{Value: v}
	}
	return result, nil
}
