package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/gitbackend"
	"github.com/continuity-lab/continuity-lab/internal/gitbrowse"
	"github.com/continuity-lab/continuity-lab/internal/health"
	"github.com/continuity-lab/continuity-lab/internal/locks"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/continuity-lab/continuity-lab/internal/snapshot"
	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
)

type Server struct {
	Config   config.Config
	Store    objectstore.ObjectStore
	Health   *health.State
	Repos    *repository.Manager
	Locks    *locks.Manager
	activeMu sync.Mutex
	active   map[string]int
}

func New(cfg config.Config, store objectstore.ObjectStore, state *health.State) (*Server, error) {
	repos := &repository.Manager{Config: cfg, Store: store}
	if err := repos.PrepareDirs(); err != nil {
		return nil, err
	}
	observability.InitNode(cfg.NodeID)
	return &Server{Config: cfg, Store: store, Health: state, Repos: repos, Locks: locks.New(filepath.Join(cfg.DataDir, "locks")), active: make(map[string]int)}, nil
}

func (s *Server) Router() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", s.Health.Healthz)
	router.Get("/readyz", s.Health.Readyz)
	router.Handle("/metrics", observability.Handler())
	router.HandleFunc("/git/*", s.git)
	router.Get("/api/v1/browse/*", s.browse)
	router.Get("/api/v1/cache", s.cacheList)
	router.Post("/api/v1/repos/*", s.nodeAdmin)
	router.Put("/api/v1/failpoints/*", s.setFailpoint)
	router.Delete("/api/v1/failpoints/*", s.clearFailpoint)
	return router
}

func (s *Server) git(w http.ResponseWriter, r *http.Request) {
	name, suffix, service, err := parseGitPath(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoID := model.RepoID(name)
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = ulid.Make().String()
		r.Header.Set("X-Request-ID", requestID)
	}
	w.Header().Set("X-Request-ID", requestID)
	capture := &responseStatus{ResponseWriter: w, status: http.StatusOK}
	w = capture
	started := time.Now()
	metricService := strings.TrimPrefix(service, "git-")
	defer func() {
		observability.GitRequests.WithLabelValues(s.Config.NodeID, metricService, strconv.Itoa(capture.status)).Inc()
		observability.GitDuration.WithLabelValues(s.Config.NodeID, metricService).Observe(time.Since(started).Seconds())
		if service == "git-receive-pack" && r.Method == http.MethodPost {
			result := "success"
			if capture.status >= 400 {
				result = "error"
			}
			observability.Pushes.WithLabelValues(s.Config.NodeID, result).Inc()
			observability.PushDuration.WithLabelValues(s.Config.NodeID).Observe(time.Since(started).Seconds())
		}
	}()
	// Freshness may materialize or replay, so it runs under the exclusive
	// guard. A synchronized read then downgrades by reacquiring a shared guard
	// before the CGI process; a writer or eviction cannot overlap that CGI.
	lockCtx, cancel := context.WithTimeout(r.Context(), s.Config.LockTimeout)
	defer cancel()
	waitStarted := time.Now()
	guard, err := s.Locks.Acquire(lockCtx, repoID, locks.Exclusive)
	observability.LockWait.WithLabelValues(s.Config.NodeID, "exclusive").Observe(time.Since(waitStarted).Seconds())
	if err != nil {
		http.Error(w, "repository lock unavailable", http.StatusServiceUnavailable)
		return
	}
	s.corruptNextPack(repoID)
	var manifest model.Manifest
	if service == "git-upload-pack" {
		manifest, _, _, err = s.Repos.EnsureFreshRead(r.Context(), name)
	} else {
		manifest, _, _, err = s.Repos.EnsureFresh(r.Context(), name)
	}
	if err != nil {
		_ = guard.Close()
		status := http.StatusServiceUnavailable
		if errors.Is(err, objectstore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, fmt.Sprintf("repository unavailable: %v", err), status)
		return
	}
	isWrite := service == "git-receive-pack" && r.Method == http.MethodPost
	if !isWrite {
		if err := guard.Close(); err != nil {
			http.Error(w, "repository lock release failed", http.StatusInternalServerError)
			return
		}
		waitStarted = time.Now()
		guard, err = s.Locks.Acquire(lockCtx, repoID, locks.Shared)
		observability.LockWait.WithLabelValues(s.Config.NodeID, "shared").Observe(time.Since(waitStarted).Seconds())
		if err != nil {
			http.Error(w, "repository read lock unavailable", http.StatusServiceUnavailable)
			return
		}
		if _, err := os.Stat(s.Repos.RepoPath(repoID)); err != nil {
			_ = guard.Close()
			http.Error(w, "repository was evicted during read synchronization", http.StatusServiceUnavailable)
			return
		}
	}
	defer guard.Close()
	s.activeMu.Lock()
	s.active[repoID]++
	s.activeMu.Unlock()
	defer func() { s.activeMu.Lock(); s.active[repoID]--; s.activeMu.Unlock() }()
	pushID := ""
	if service == "git-receive-pack" && r.Method == http.MethodPost {
		pushID = ulid.Make().String()
	}
	env := gitbackend.Environment{ProjectRoot: filepath.Join(s.Config.DataDir, "repos"), RequestID: requestID, PathInfo: "/" + repoID + ".git" + suffix, PushID: pushID, RepoID: repoID, RepoName: manifest.Name, NodeID: s.Config.NodeID, DataDir: s.Config.DataDir}
	if err := gitbackend.Serve(r.Context(), w, r, env); err != nil {
		slog.Error("git backend failed", "error", err, "request_id", r.Header.Get("X-Request-ID"), "push_id", pushID, "repo_id", repoID, "repo_name", name, "latency_ms", time.Since(started).Milliseconds())
	}
}

func parseGitPath(r *http.Request) (name, suffix, service string, err error) {
	escaped := strings.ToLower(r.URL.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") {
		return "", "", "", errors.New("encoded separators are forbidden")
	}
	if !strings.HasPrefix(r.URL.Path, "/git/") {
		return "", "", "", errors.New("invalid Git path")
	}
	path := strings.TrimPrefix(r.URL.Path, "/git/")
	for _, candidate := range []string{"/info/refs", "/git-upload-pack", "/git-receive-pack"} {
		if strings.HasSuffix(path, candidate) {
			suffix = candidate
			path = strings.TrimSuffix(path, candidate)
			break
		}
	}
	if suffix == "" {
		return "", "", "", errors.New("unsupported Smart HTTP path")
	}
	name, err = model.CanonicalName(path)
	if err != nil {
		return "", "", "", err
	}
	if suffix == "/info/refs" {
		service = r.URL.Query().Get("service")
		if service != "git-upload-pack" && service != "git-receive-pack" {
			return "", "", "", errors.New("invalid Git service")
		}
	} else {
		service = strings.TrimPrefix(suffix, "/")
	}
	if service == "git-upload-pack" && r.Method != http.MethodGet && r.Method != http.MethodPost {
		return "", "", "", errors.New("invalid method")
	}
	if service == "git-receive-pack" && r.Method != http.MethodGet && r.Method != http.MethodPost {
		return "", "", "", errors.New("invalid method")
	}
	return name, suffix, service, nil
}

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) corruptNextPack(repoID string) {
	marker := filepath.Join(s.Config.DataDir, "failpoints", "corrupt_next_local_pack")
	if _, err := os.Stat(marker); err != nil {
		return
	}
	_ = os.Remove(marker)
	root := filepath.Join(s.Repos.RepoPath(repoID), "objects", "pack")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".pack") {
			return nil
		}
		file, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr == nil {
			_, _ = file.WriteAt([]byte("CORRUPT"), 16)
			_ = file.Sync()
			_ = file.Close()
		}
		return filepath.SkipAll
	})
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	name, err := model.CanonicalName(strings.TrimPrefix(chi.URLParam(r, "*"), "/"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoID := model.RepoID(name)
	lockCtx, cancel := context.WithTimeout(r.Context(), s.Config.LockTimeout)
	defer cancel()
	guard, err := s.Locks.Acquire(lockCtx, repoID, locks.Exclusive)
	if err != nil {
		http.Error(w, "repository lock unavailable", http.StatusServiceUnavailable)
		return
	}
	manifest, _, _, err := s.Repos.EnsureFreshRead(r.Context(), name)
	if closeErr := guard.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, objectstore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, fmt.Sprintf("repository unavailable: %v", err), status)
		return
	}
	guard, err = s.Locks.Acquire(lockCtx, repoID, locks.Shared)
	if err != nil {
		http.Error(w, "repository read lock unavailable", http.StatusServiceUnavailable)
		return
	}
	defer guard.Close()

	repoPath := s.Repos.RepoPath(repoID)
	view := r.URL.Query().Get("view")
	revision := r.URL.Query().Get("ref")
	if revision == "" {
		revision = manifest.DefaultBranch
	}
	var result any
	switch view {
	case "refs":
		result, err = gitbrowse.ListRefs(r.Context(), repoPath, manifest.DefaultBranch)
	case "tree":
		result, err = gitbrowse.ListTree(r.Context(), repoPath, revision, r.URL.Query().Get("path"))
	case "blob":
		result, err = gitbrowse.ReadBlob(r.Context(), repoPath, revision, r.URL.Query().Get("path"))
	case "commits":
		limit := 0
		if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
			limit, err = strconv.Atoi(rawLimit)
			if err != nil {
				http.Error(w, "invalid commit limit", http.StatusBadRequest)
				return
			}
		}
		result, err = gitbrowse.ListCommits(r.Context(), repoPath, revision, limit)
	case "commit":
		result, err = gitbrowse.GetCommit(r.Context(), repoPath, r.URL.Query().Get("oid"))
	default:
		http.Error(w, "unsupported repository view", http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gitbrowse.ErrInvalidPath) || errors.Is(err, gitbrowse.ErrInvalidRevision) {
			status = http.StatusBadRequest
		} else if errors.Is(err, gitbrowse.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	health.JSON(w, http.StatusOK, result)
}

func (s *Server) cacheList(w http.ResponseWriter, _ *http.Request) {
	entries, _ := os.ReadDir(filepath.Join(s.Config.DataDir, "state"))
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			out = append(out, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	health.JSON(w, http.StatusOK, map[string]any{"node_id": s.Config.NodeID, "repos": out})
}

func (s *Server) nodeAdmin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "unsupported node operation", http.StatusNotFound)
		return
	}
	operation := parts[len(parts)-1]
	name, err := model.CanonicalName(strings.TrimSuffix(path, "/"+operation))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoID := model.RepoID(name)
	lockCtx, cancel := context.WithTimeout(r.Context(), s.Config.LockTimeout)
	defer cancel()
	guard, err := s.Locks.Acquire(lockCtx, repoID, locks.Exclusive)
	if err != nil {
		http.Error(w, "repository lock unavailable", http.StatusServiceUnavailable)
		return
	}
	defer guard.Close()
	switch operation {
	case "verify":
		head, err := s.Repos.Verify(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		health.JSON(w, http.StatusOK, map[string]any{"node_id": s.Config.NodeID, "repo_id": repoID, "sequence": head.Sequence, "verified": true})
	case "compact":
		metadata, etag, err := (snapshot.Compactor{NodeID: s.Config.NodeID, Store: s.Store, Repos: s.Repos}).Compact(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		health.JSON(w, http.StatusOK, map[string]any{"node_id": s.Config.NodeID, "repo_id": repoID, "snapshot": metadata, "head_etag": etag})
	case "gc":
		result, err := snapshot.GC(r.Context(), s.Store, repoID, s.Config.GCGracePeriod, snapshot.ParseDryRun(r.URL.Query().Get("dry_run")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		health.JSON(w, http.StatusOK, result)
	case "evict":
		s.activeMu.Lock()
		active := s.active[repoID]
		s.activeMu.Unlock()
		if active != 0 {
			http.Error(w, "repository has active requests", http.StatusConflict)
			return
		}
		if _, _, err := s.Repos.Head(r.Context(), repoID); err != nil {
			http.Error(w, "object store unavailable", http.StatusServiceUnavailable)
			return
		}
		repoPath := s.Repos.RepoPath(repoID)
		tombstone := filepath.Join(s.Config.DataDir, "staging", repoID+"-evicted-"+ulid.Make().String())
		if _, err := os.Stat(repoPath); err == nil {
			if err := os.Rename(repoPath, tombstone); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := os.Remove(s.Repos.StatePath(repoID)); err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.RemoveAll(tombstone); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		health.JSON(w, http.StatusOK, map[string]any{"node_id": s.Config.NodeID, "repo_id": repoID, "evicted": true})
	default:
		http.Error(w, "unsupported node operation", http.StatusNotFound)
	}
}

func (s *Server) setFailpoint(w http.ResponseWriter, r *http.Request) {
	if !s.Config.LabMode {
		http.Error(w, "failpoints require lab mode", http.StatusForbidden)
		return
	}
	name := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if name == "" || strings.ContainsAny(name, "/\\.") {
		http.Error(w, "invalid failpoint", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(s.Config.DataDir, "failpoints")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("once\n"), 0o640); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	health.JSON(w, http.StatusOK, map[string]any{"name": name, "mode": "once"})
}

func (s *Server) clearFailpoint(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	_ = os.Remove(filepath.Join(s.Config.DataDir, "failpoints", name))
	health.JSON(w, http.StatusOK, map[string]any{"name": name, "mode": "off"})
}
