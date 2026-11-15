// Package store holds all DynamoDB access for folders and notes (single-table design).
package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// ErrNotFound is returned when a folder or note doesn't exist.
var ErrNotFound = errors.New("not found")

const userPK = "USER#demo" // single demo user; no auth (documented tradeoff)

type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

type Note struct {
	ID        string   `json:"id"`
	FolderID  string   `json:"folderId"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	PhotoKeys []string `json:"photoKeys"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

// item is the raw DynamoDB shape: PK/SK plus the attributes of either item type.
type item struct {
	PK        string   `dynamodbav:"PK"`
	SK        string   `dynamodbav:"SK"`
	Name      string   `dynamodbav:"name,omitempty"`
	Title     string   `dynamodbav:"title,omitempty"`
	Text      string   `dynamodbav:"text,omitempty"`
	PhotoKeys []string `dynamodbav:"photoKeys,omitempty"`
	CreatedAt string   `dynamodbav:"createdAt"`
	UpdatedAt string   `dynamodbav:"updatedAt,omitempty"`
}

type Store struct {
	db    *dynamodb.Client
	table string
}

func New(db *dynamodb.Client, table string) *Store { return &Store{db: db, table: table} }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// EnsureTable creates the table if missing (used locally against DynamoDB Local).
func (s *Store) EnsureTable(ctx context.Context) error {
	_, err := s.db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &s.table})
	if err == nil {
		return nil
	}
	var nf *types.ResourceNotFoundException
	if !errors.As(err, &nf) {
		return err
	}
	_, err = s.db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   &s.table,
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
	})
	return err
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: pk},
		"SK": &types.AttributeValueMemberS{Value: sk},
	}
}

// query returns all items under pk whose SK starts with prefix.
func (s *Store) query(ctx context.Context, pk, prefix string) ([]item, error) {
	var out []item
	p := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
		TableName:              &s.table,
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :p)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
			":p":  &types.AttributeValueMemberS{Value: prefix},
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		var items []item
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &items); err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// ---- Folders ----

func (s *Store) CreateFolder(ctx context.Context, name string) (Folder, error) {
	f := Folder{ID: uuid.NewString(), Name: name, CreatedAt: now()}
	av, err := attributevalue.MarshalMap(item{PK: userPK, SK: "FOLDER#" + f.ID, Name: name, CreatedAt: f.CreatedAt})
	if err != nil {
		return Folder{}, err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: av})
	return f, err
}

func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	items, err := s.query(ctx, userPK, "FOLDER#")
	if err != nil {
		return nil, err
	}
	out := make([]Folder, 0, len(items))
	for _, it := range items {
		out = append(out, Folder{ID: strings.TrimPrefix(it.SK, "FOLDER#"), Name: it.Name, CreatedAt: it.CreatedAt})
	}
	return out, nil
}

func (s *Store) FolderExists(ctx context.Context, folderID string) (bool, error) {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: key(userPK, "FOLDER#"+folderID)})
	if err != nil {
		return false, err
	}
	return res.Item != nil, nil
}

// DeleteFolder deletes the folder item and all its notes. It returns the photo
// keys of the deleted notes so the caller can remove them from S3.
func (s *Store) DeleteFolder(ctx context.Context, folderID string) ([]string, error) {
	ok, err := s.FolderExists(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	notes, err := s.query(ctx, "FOLDER#"+folderID, "NOTE#")
	if err != nil {
		return nil, err
	}
	var photos []string
	for _, n := range notes {
		photos = append(photos, n.PhotoKeys...)
		if _, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &s.table, Key: key(n.PK, n.SK)}); err != nil {
			return nil, err
		}
	}
	_, err = s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &s.table, Key: key(userPK, "FOLDER#"+folderID)})
	return photos, err
}

// ---- Notes ----

func toNote(folderID string, it item) Note {
	keys := it.PhotoKeys
	if keys == nil {
		keys = []string{}
	}
	return Note{
		ID: strings.TrimPrefix(it.SK, "NOTE#"), FolderID: folderID, Title: it.Title, Text: it.Text,
		PhotoKeys: keys, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
	}
}

func (s *Store) CreateNote(ctx context.Context, folderID, title, text string) (Note, error) {
	ok, err := s.FolderExists(ctx, folderID)
	if err != nil {
		return Note{}, err
	}
	if !ok {
		return Note{}, ErrNotFound
	}
	t := now()
	it := item{PK: "FOLDER#" + folderID, SK: "NOTE#" + uuid.NewString(), Title: title, Text: text, CreatedAt: t, UpdatedAt: t}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return Note{}, err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: av})
	return toNote(folderID, it), err
}

func (s *Store) ListNotes(ctx context.Context, folderID string) ([]Note, error) {
	items, err := s.query(ctx, "FOLDER#"+folderID, "NOTE#")
	if err != nil {
		return nil, err
	}
	out := make([]Note, 0, len(items))
	for _, it := range items {
		out = append(out, toNote(folderID, it))
	}
	return out, nil
}

func (s *Store) GetNote(ctx context.Context, folderID, noteID string) (Note, error) {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: key("FOLDER#"+folderID, "NOTE#"+noteID)})
	if err != nil {
		return Note{}, err
	}
	if res.Item == nil {
		return Note{}, ErrNotFound
	}
	var it item
	if err := attributevalue.UnmarshalMap(res.Item, &it); err != nil {
		return Note{}, err
	}
	return toNote(folderID, it), nil
}

// update runs an UpdateItem that only succeeds if the note exists.
func (s *Store) update(ctx context.Context, folderID, noteID, expr string, names map[string]string, vals map[string]types.AttributeValue) (Note, error) {
	vals[":u"] = &types.AttributeValueMemberS{Value: now()}
	res, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.table,
		Key:                       key("FOLDER#"+folderID, "NOTE#"+noteID),
		UpdateExpression:          aws.String(expr + ", updatedAt = :u"),
		ConditionExpression:       aws.String("attribute_exists(PK)"),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: vals,
		ReturnValues:              types.ReturnValueAllNew,
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}
	var it item
	if err := attributevalue.UnmarshalMap(res.Attributes, &it); err != nil {
		return Note{}, err
	}
	return toNote(folderID, it), nil
}

func (s *Store) UpdateNote(ctx context.Context, folderID, noteID, title, text string) (Note, error) {
	return s.update(ctx, folderID, noteID, "SET title = :ti, #t = :tx", map[string]string{"#t": "text"}, map[string]types.AttributeValue{
		":ti": &types.AttributeValueMemberS{Value: title},
		":tx": &types.AttributeValueMemberS{Value: text},
	})
}

// AddPhoto appends an S3 key to the note's photoKeys list.
func (s *Store) AddPhoto(ctx context.Context, folderID, noteID, photoKey string) (Note, error) {
	return s.update(ctx, folderID, noteID, "SET photoKeys = list_append(if_not_exists(photoKeys, :empty), :k)", nil, map[string]types.AttributeValue{
		":empty": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
		":k":     &types.AttributeValueMemberL{Value: []types.AttributeValue{&types.AttributeValueMemberS{Value: photoKey}}},
	})
}

// DeleteNote deletes a note and returns its photo keys for S3 cleanup.
func (s *Store) DeleteNote(ctx context.Context, folderID, noteID string) ([]string, error) {
	res, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:    &s.table,
		Key:          key("FOLDER#"+folderID, "NOTE#"+noteID),
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return nil, err
	}
	if res.Attributes == nil {
		return nil, ErrNotFound
	}
	var it item
	if err := attributevalue.UnmarshalMap(res.Attributes, &it); err != nil {
		return nil, err
	}
	return it.PhotoKeys, nil
}
