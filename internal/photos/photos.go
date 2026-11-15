// Package photos wraps S3: presigned upload/view URLs and deletes.
// Photo bytes never pass through our API; the browser talks to S3 directly.
package photos

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

const (
	MaxBytes  = 5 << 20 // 5 MB cap per photo
	uploadTTL = 5 * time.Minute
	viewTTL   = time.Hour
)

// Allowed content types -> file extension.
var allowed = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

var ErrBadType = errors.New("contentType must be image/jpeg, image/png or image/webp")
var ErrTooBig = errors.New("size must be between 1 byte and 5 MB")

type Photos struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// New takes two clients: client for the API's own calls (HeadObject, Delete) and
// public for signing URLs. They differ only in Docker, where the API reaches S3 at
// http://localstack:4566 but the browser must use http://localhost:4566. The host is
// part of the signature, so the URL must be signed for the host the browser will use.
func New(client, public *s3.Client, bucket string) *Photos {
	return &Photos{client: client, presign: s3.NewPresignClient(public), bucket: bucket}
}

// UploadURL returns a presigned PUT URL for a new photo of the note, plus its S3 key.
// Content type and exact size are part of the signature, so S3 rejects an upload
// that sends a different type or a different number of bytes.
func (p *Photos) UploadURL(ctx context.Context, noteID, contentType string, size int64) (url, key string, err error) {
	ext, ok := allowed[contentType]
	if !ok {
		return "", "", ErrBadType
	}
	if size <= 0 || size > MaxBytes {
		return "", "", ErrTooBig
	}
	key = KeyPrefix(noteID) + uuid.NewString() + "." + ext
	req, err := p.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        &p.bucket,
		Key:           &key,
		ContentType:   &contentType,
		ContentLength: &size,
	}, s3.WithPresignExpires(uploadTTL))
	if err != nil {
		return "", "", err
	}
	return req.URL, key, nil
}

// ViewURL returns a short-lived presigned GET URL so the browser can show a private photo.
func (p *Photos) ViewURL(ctx context.Context, key string) (string, error) {
	req, err := p.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &p.bucket, Key: &key},
		s3.WithPresignExpires(viewTTL))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// Exists checks the object is really in S3 (used to confirm an upload happened).
func (p *Photos) Exists(ctx context.Context, key string) (bool, error) {
	_, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &p.bucket, Key: &key})
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return false, nil
	}
	return err == nil, err
}

// Delete removes photos in one batch call (S3 allows up to 1000 keys per call).
func (p *Photos) Delete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objs := make([]types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		objs[i] = types.ObjectIdentifier{Key: aws.String(k)}
	}
	_, err := p.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: &p.bucket,
		Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
	})
	return err
}

// KeyPrefix is where a note's photos live: notes/<noteId>/
func KeyPrefix(noteID string) string { return "notes/" + noteID + "/" }
