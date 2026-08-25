package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/health"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/continuity-lab/continuity-lab/internal/routing"
	"github.com/continuity-lab/continuity-lab/internal/webedit"
	"github.com/continuity-lab/continuity-lab/internal/webui"
	"github.com/go-chi/chi/v5"
)

type nodeStatus struct {
	Node      routing.Node `json:"node"`
	Healthy   bool         `json:"healthy"`
	CheckedAt time.Time    `json:"checked_at"`
}

type Server struct {
	Config     config.Config
	Store      objectstore.ObjectStore
	Health     *health.State
	Repos      admin.Repositories
	Editor     webedit.Editor
	mu         sync.RWMutex
	nodes      map[string]nodeStatus
	roundRobin atomic.Uint64
	client     *http.Client
}

func New(cfg config.Config, store objectstore.ObjectStore, state *health.State) *Server {
	observability.InitNode("gateway")
	nodes := make(map[string]nodeStatus, len(cfg.Nodes))
	for _, item := range cfg.Nodes {
		node := routing.Node{ID: item.ID, URL: item.URL}
		nodes[item.ID] = nodeStatus{Node: node}
	}
	return &Server{Config: cfg, Store: store, Health: state, Repos: admin.Repositories{Store: store}, Editor: webedit.Editor{GatewayURL: cfg.GatewayURL}, nodes: nodes, client: &http.Client{Timeout: 2 * time.Second}}
}

func (s *Server) Router() http.Handler {
	router := chi.NewRouter()
	ui := webui.Handler()
	router.Handle("/assets/*", ui)
	router.Get("/sw.js", ui.ServeHTTP)
	router.Head("/sw.js", ui.ServeHTTP)
	router.Get("/", ui.ServeHTTP)
	router.Head("/", ui.ServeHTTP)
	router.Get("/repos/*", ui.ServeHTTP)
	router.Head("/repos/*", ui.ServeHTTP)
	router.Get("/admin", ui.ServeHTTP)
	router.Head("/admin", ui.ServeHTTP)
	router.Get("/admin/*", ui.ServeHTTP)
	router.Head("/admin/*", ui.ServeHTTP)
	router.Get("/healthz", s.Health.Healthz)
	router.Get("/readyz", s.Health.Readyz)
	router.Handle("/metrics", observability.Handler())
	router.Get("/api/v1/cluster", s.cluster)
	router.Get("/api/v1/repos", s.listRepos)
	router.Post("/api/v1/repos", s.createRepo)
	router.Get("/api/v1/repos/*", s.inspectRepo)
	router.Get("/api/v1/browse/*", s.browse)
	router.Post("/api/v1/edit/*", s.editFile)
	router.Post("/api/v1/repos/*", s.repoAction)
	router.HandleFunc("/git/*", s.git)
	return requestID(router)
}

func (s *Server) RunHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.checkNodes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) checkNodes(ctx context.Context) {
	storeCtx, cancel := context.WithTimeout(ctx, time.Second)
	storeErr := s.Store.EnsureBucket(storeCtx)
	cancel()
	s.mu.RLock()
	statuses := make([]nodeStatus, 0, len(s.nodes))
	for _, status := range s.nodes {
		statuses = append(statuses, status)
	}
	s.mu.RUnlock()
	healthy := 0
	for _, status := range statuses {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, status.Node.URL+"/readyz", nil)
		response, err := s.client.Do(request)
		ok := err == nil && response.StatusCode == http.StatusOK
		if response != nil {
			_ = response.Body.Close()
		}
		status.Healthy, status.CheckedAt = ok, time.Now().UTC()
		s.mu.Lock()
		s.nodes[status.Node.ID] = status
		s.mu.Unlock()
		if ok {
			healthy++
		}
	}
	s.Health.SetReady(healthy > 0 && storeErr == nil)
}

func (s *Server) git(w http.ResponseWriter, r *http.Request) {
	name, err := repoNameFromGitPath(r.URL.Path)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_repo_name", err.Error())
		return
	}
	ranked := routing.Rank(model.RepoID(name), s.healthyNodes())
	if len(ranked) == 0 {
		writeError(w, r, http.StatusServiceUnavailable, "no_healthy_node", "no Git node is ready")
		return
	}
	isRead := strings.Contains(r.URL.Path, "git-upload-pack") || r.URL.Query().Get("service") == "git-upload-pack"
	chosen := ranked[0]
	if isRead {
		limit := min(s.Config.ReadReplicaCount, len(ranked))
		chosen = ranked[int(s.roundRobin.Add(1)-1)%limit]
	}
	target, _ := url.Parse(chosen.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
		writeError(writer, request, http.StatusBadGateway, "node_unavailable", proxyErr.Error())
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) healthyNodes() []routing.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]routing.Node, 0, len(s.nodes))
	for _, status := range s.nodes {
		if status.Healthy {
			out = append(out, status.Node)
		}
	}
	return out
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repositories, err := s.Repos.List(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "repo_list_failed", err.Error())
		return
	}
	health.JSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	name, err := model.CanonicalName(strings.TrimPrefix(chi.URLParam(r, "*"), "/"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_repo_name", err.Error())
		return
	}
	ranked := routing.Rank(model.RepoID(name), s.healthyNodes())
	if len(ranked) == 0 {
		writeError(w, r, http.StatusServiceUnavailable, "no_healthy_node", "no Git node is ready")
		return
	}
	limit := min(s.Config.ReadReplicaCount, len(ranked))
	chosen := ranked[int(s.roundRobin.Add(1)-1)%limit]
	target, _ := url.Parse(chosen.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
		writeError(writer, request, http.StatusBadGateway, "node_unavailable", proxyErr.Error())
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) editFile(w http.ResponseWriter, r *http.Request) {
	name, err := model.CanonicalName(strings.TrimPrefix(chi.URLParam(r, "*"), "/"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_repo_name", err.Error())
		return
	}
	var input webedit.Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*webedit.MaxContentSize+(64<<10)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	editCtx, cancel := context.WithTimeout(r.Context(), s.Config.LockTimeout)
	defer cancel()
	result, editErr := s.Editor.Commit(editCtx, name, input)

	info, inspectErr := s.Repos.Inspect(r.Context(), name)
	if editErr != nil && result.CommitOID != "" && inspectErr == nil && info.Head.Refs[input.Branch] == result.CommitOID {
		editErr = nil
	}
	if editErr != nil {
		status, code := http.StatusServiceUnavailable, "edit_publish_failed"
		switch {
		case errors.Is(editErr, webedit.ErrInvalidInput):
			status, code = http.StatusBadRequest, "invalid_edit"
		case errors.Is(editErr, webedit.ErrFileNotFound):
			status, code = http.StatusNotFound, "file_not_found"
		case errors.Is(editErr, webedit.ErrConflict), errors.Is(editErr, webedit.ErrFileExists):
			status, code = http.StatusConflict, "edit_conflict"
		case inspectErr == nil && info.Head.Refs[input.Branch] != input.BaseCommit:
			status, code = http.StatusConflict, "edit_conflict"
		}
		writeError(w, r, status, code, editErr.Error())
		return
	}
	if inspectErr != nil {
		info, inspectErr = s.Repos.Inspect(r.Context(), name)
	}
	response := map[string]any{"commit_oid": result.CommitOID, "branch": result.Branch, "path": result.Path, "created": result.Created}
	if inspectErr == nil {
		response["sequence"] = info.Head.Sequence
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	health.JSON(w, status, response)
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	info, err := s.Repos.Create(r.Context(), input.Name, input.DefaultBranch)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "repo_create_failed", err.Error())
		return
	}
	health.JSON(w, http.StatusCreated, map[string]any{"repo_id": info.Manifest.RepoID, "name": info.Manifest.Name, "clone_url": strings.TrimRight(s.Config.GatewayURL, "/") + "/git/" + info.Manifest.Name + ".git", "sequence": info.Head.Sequence})
}

func (s *Server) inspectRepo(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	operation := "inspect"
	for _, suffix := range []string{"/wal", "/refs"} {
		if strings.HasSuffix(path, suffix) {
			operation = strings.TrimPrefix(suffix, "/")
			path = strings.TrimSuffix(path, suffix)
		}
	}
	name, err := model.CanonicalName(path)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_repo_name", err.Error())
		return
	}
	info, err := s.Repos.Inspect(r.Context(), name)
	if err != nil {
		status, code := http.StatusInternalServerError, "repo_inspect_failed"
		if errors.Is(err, objectstore.ErrNotFound) {
			status, code = http.StatusNotFound, "repo_not_found"
		}
		writeError(w, r, status, code, err.Error())
		return
	}
	if operation == "refs" {
		health.JSON(w, http.StatusOK, map[string]any{"repo_id": info.Manifest.RepoID, "sequence": info.Head.Sequence, "refs": info.Head.Refs})
		return
	}
	if operation == "wal" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = int(info.Head.Sequence)
		}
		entries := make([]model.WALEntry, 0, min(limit, int(info.Head.Sequence)))
		key := info.Head.LatestEntryKey
		for key != "" && len(entries) < limit {
			entry, _, err := admin.GetJSON[model.WALEntry](r.Context(), s.Store, key)
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, "wal_corrupt", err.Error())
				return
			}
			entries = append(entries, entry)
			key = entry.ParentEntryKey
		}
		health.JSON(w, http.StatusOK, map[string]any{"repo_id": info.Manifest.RepoID, "sequence": info.Head.Sequence, "entries_newest_first": entries})
		return
	}
	health.JSON(w, http.StatusOK, map[string]any{"manifest": info.Manifest, "head": info.Head, "head_etag": info.HeadETag})
}

func (s *Server) repoAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		writeError(w, r, http.StatusNotFound, "unknown_action", "missing repository action")
		return
	}
	action := parts[len(parts)-1]
	if action != "compact" && action != "verify" && action != "gc" {
		writeError(w, r, http.StatusNotFound, "unknown_action", action)
		return
	}
	name, err := model.CanonicalName(strings.TrimSuffix(path, "/"+action))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_repo_name", err.Error())
		return
	}
	ranked := routing.Rank(model.RepoID(name), s.healthyNodes())
	if len(ranked) == 0 {
		writeError(w, r, http.StatusServiceUnavailable, "no_healthy_node", "no Git node is ready")
		return
	}
	target, _ := url.Parse(ranked[0].URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
		writeError(writer, request, http.StatusBadGateway, "node_unavailable", proxyErr.Error())
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) cluster(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	out := make([]nodeStatus, 0, len(s.nodes))
	for _, status := range s.nodes {
		out = append(out, status)
	}
	s.mu.RUnlock()
	health.JSON(w, http.StatusOK, map[string]any{"nodes": out})
}

func repoNameFromGitPath(path string) (string, error) {
	if !strings.HasPrefix(path, "/git/") {
		return "", errors.New("missing /git prefix")
	}
	value := strings.TrimPrefix(path, "/git/")
	for _, suffix := range []string{"/info/refs", "/git-upload-pack", "/git-receive-pack"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return model.CanonicalName(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	health.JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": r.Header.Get("X-Request-ID")}})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		if r.Header.Get("X-Request-ID") == "" {
			r.Header.Set("X-Request-ID", fmt.Sprintf("%d", time.Now().UnixNano()))
		}
		w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		capture := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(capture, r)
		slog.Info("gateway request", "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "path", r.URL.Path, "status", capture.status, "latency_ms", time.Since(started).Milliseconds())
	})
}
