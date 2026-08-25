package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
)

type Repositories struct {
	Store objectstore.ObjectStore
	Now   func() time.Time
}

type RepoInfo struct {
	Manifest     model.Manifest
	ManifestETag objectstore.ETag
	Head         model.Head
	HeadETag     objectstore.ETag
}

type RepoSummary struct {
	RepoID        string    `json:"repo_id"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Sequence      uint64    `json:"sequence"`
	RefCount      int       `json:"ref_count"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (r Repositories) List(ctx context.Context) ([]RepoSummary, error) {
	objects, err := r.Store.List(ctx, "repos/")
	if err != nil {
		return nil, err
	}
	result := make([]RepoSummary, 0)
	for _, object := range objects {
		if !strings.HasSuffix(object.Key, "/manifest.json") {
			continue
		}
		manifest, manifestETag, err := getJSON[model.Manifest](ctx, r.Store, object.Key)
		if err != nil {
			return nil, err
		}
		if manifestETag == "" {
			return nil, fmt.Errorf("manifest %s has no ETag", object.Key)
		}
		if err := manifest.Validate(); err != nil {
			return nil, fmt.Errorf("validate %s: %w", object.Key, err)
		}
		head, headETag, err := getJSON[model.Head](ctx, r.Store, model.HeadKey(manifest.RepoID))
		if err != nil {
			return nil, err
		}
		if headETag == "" {
			return nil, fmt.Errorf("head for %s has no ETag", manifest.Name)
		}
		if err := head.Validate(); err != nil {
			return nil, fmt.Errorf("validate head for %s: %w", manifest.Name, err)
		}
		result = append(result, RepoSummary{RepoID: manifest.RepoID, Name: manifest.Name, DefaultBranch: manifest.DefaultBranch, Sequence: head.Sequence, RefCount: len(head.Refs), UpdatedAt: head.UpdatedAt})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (r Repositories) Create(ctx context.Context, rawName, defaultBranch string) (RepoInfo, error) {
	name, err := model.CanonicalName(rawName)
	if err != nil {
		return RepoInfo{}, err
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	defaultBranch = strings.TrimPrefix(defaultBranch, "refs/heads/")
	if strings.Contains(defaultBranch, "/") || defaultBranch == "" {
		return RepoInfo{}, errors.New("default branch must be a simple branch name")
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	repoID := model.RepoID(name)
	manifest := model.Manifest{SchemaVersion: model.SchemaVersion, RepoID: repoID, Name: name, ObjectFormat: "sha1", DefaultBranch: "refs/heads/" + defaultBranch, CreatedAt: now}
	manifestBody, _ := model.Marshal(manifest)
	_, manifestETag, err := r.Store.PutImmutable(ctx, model.ManifestKey(repoID), bytes.NewReader(manifestBody), int64(len(manifestBody)), "application/json")
	if err != nil {
		return RepoInfo{}, fmt.Errorf("persist manifest: %w", err)
	}
	storedManifest, storedManifestETag, err := getJSON[model.Manifest](ctx, r.Store, model.ManifestKey(repoID))
	if err != nil {
		return RepoInfo{}, err
	}
	if storedManifest.Name != manifest.Name || storedManifest.ObjectFormat != manifest.ObjectFormat || storedManifest.RepoID != manifest.RepoID {
		return RepoInfo{}, errors.New("existing manifest conflicts with requested repository")
	}
	manifestETag = storedManifestETag

	head := model.Head{SchemaVersion: model.SchemaVersion, RepoID: repoID, Refs: map[string]string{}, UpdatedAt: now}
	headBody, _ := model.Marshal(head)
	_, headETag, err := r.Store.PutImmutable(ctx, model.HeadKey(repoID), bytes.NewReader(headBody), int64(len(headBody)), "application/json")
	if err != nil {
		return RepoInfo{}, fmt.Errorf("persist initial head: %w", err)
	}
	storedHead, storedHeadETag, err := getJSON[model.Head](ctx, r.Store, model.HeadKey(repoID))
	if err != nil {
		return RepoInfo{}, err
	}
	if storedHead.RepoID != repoID {
		return RepoInfo{}, errors.New("existing head conflicts with repository")
	}
	headETag = storedHeadETag
	return RepoInfo{Manifest: storedManifest, ManifestETag: manifestETag, Head: storedHead, HeadETag: headETag}, nil
}

func (r Repositories) Inspect(ctx context.Context, rawName string) (RepoInfo, error) {
	name, err := model.CanonicalName(rawName)
	if err != nil {
		return RepoInfo{}, err
	}
	repoID := model.RepoID(name)
	manifest, manifestETag, err := getJSON[model.Manifest](ctx, r.Store, model.ManifestKey(repoID))
	if err != nil {
		return RepoInfo{}, err
	}
	if err := manifest.Validate(); err != nil || manifest.Name != name {
		return RepoInfo{}, errors.New("invalid repository manifest")
	}
	head, headETag, err := getJSON[model.Head](ctx, r.Store, model.HeadKey(repoID))
	if err != nil {
		return RepoInfo{}, err
	}
	if err := head.Validate(); err != nil || head.RepoID != repoID {
		return RepoInfo{}, errors.New("invalid repository head")
	}
	return RepoInfo{Manifest: manifest, ManifestETag: manifestETag, Head: head, HeadETag: headETag}, nil
}

func getJSON[T any](ctx context.Context, store objectstore.ObjectStore, key string) (T, objectstore.ETag, error) {
	var value T
	obj, err := store.Get(ctx, key)
	if err != nil {
		return value, "", err
	}
	defer obj.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(obj.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, "", fmt.Errorf("decode %s: %w", key, err)
	}
	return value, obj.ETag, nil
}

func GetJSON[T any](ctx context.Context, store objectstore.ObjectStore, key string) (T, objectstore.ETag, error) {
	return getJSON[T](ctx, store, key)
}
