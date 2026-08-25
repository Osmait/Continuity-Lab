package objectstore

import (
	"context"
	"errors"
	"io"
	"time"
)

type ETag string

type Object struct {
	Key         string
	Body        io.ReadCloser
	ETag        ETag
	Size        int64
	ContentType string
}

type ObjectInfo struct {
	Key          string
	ETag         ETag
	Size         int64
	LastModified time.Time
}

type ObjectStore interface {
	EnsureBucket(ctx context.Context) error
	Get(ctx context.Context, key string) (Object, error)
	GetIfChanged(ctx context.Context, key string, known ETag) (obj *Object, notModified bool, err error)
	Head(ctx context.Context, key string) (etag ETag, size int64, err error)
	PutImmutable(ctx context.Context, key string, body io.Reader, size int64, contentType string) (created bool, etag ETag, err error)
	PutIfMatch(ctx context.Context, key string, expected ETag, body io.Reader, size int64, contentType string) (newETag ETag, err error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

var (
	ErrNotFound         = errors.New("object not found")
	ErrPrecondition     = errors.New("precondition failed")
	ErrConflict         = errors.New("conditional write conflict")
	ErrStoreUnavailable = errors.New("object store unavailable")
)
