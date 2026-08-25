package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

func Conformance(ctx context.Context, store ObjectStore) error {
	prefix := fmt.Sprintf("conformance/%d", time.Now().UnixNano())
	defer func() { _ = store.Delete(context.WithoutCancel(ctx), prefix) }()

	created, firstETag, err := store.PutImmutable(ctx, prefix, bytes.NewReader([]byte("first")), 5, "text/plain")
	if err != nil || !created || firstETag == "" {
		return fmt.Errorf("create with If-None-Match: %w", err)
	}
	if _, err = store.PutIfMatch(ctx, prefix, ETag(`"deliberately-wrong"`), bytes.NewReader([]byte("bad")), 3, "text/plain"); !errors.Is(err, ErrPrecondition) {
		return fmt.Errorf("wrong If-Match did not produce precondition failure: %w", err)
	}
	second := []byte("second")
	secondETag, err := store.PutIfMatch(ctx, prefix, firstETag, bytes.NewReader(second), int64(len(second)), "text/plain")
	if err != nil || secondETag == "" || secondETag == firstETag {
		return fmt.Errorf("correct If-Match: %w", err)
	}
	obj, notModified, err := store.GetIfChanged(ctx, prefix, secondETag)
	if err != nil || !notModified || obj != nil {
		return fmt.Errorf("conditional GET did not return 304 semantics: %w", err)
	}
	created, _, err = store.PutImmutable(ctx, prefix, bytes.NewReader([]byte("overwrite")), 9, "text/plain")
	if err != nil || created {
		return fmt.Errorf("repeated immutable PUT: created=%v err=%w", created, err)
	}
	got, err := store.Get(ctx, prefix)
	if err != nil {
		return err
	}
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	if err != nil || !bytes.Equal(body, second) {
		return fmt.Errorf("immutable PUT overwrote object: body=%q err=%w", body, err)
	}
	return nil
}
