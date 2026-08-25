package gitbackend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Environment struct {
	ProjectRoot string
	RequestID   string
	PathInfo    string
	PushID      string
	RepoID      string
	RepoName    string
	NodeID      string
	DataDir     string
}

func Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, env Environment) error {
	command := exec.CommandContext(ctx, "git", "http-backend")
	values := map[string]string{
		"GIT_PROJECT_ROOT":      env.ProjectRoot,
		"GIT_HTTP_EXPORT_ALL":   "1",
		"PATH_INFO":             env.PathInfo,
		"REQUEST_METHOD":        r.Method,
		"QUERY_STRING":          r.URL.RawQuery,
		"CONTENT_TYPE":          r.Header.Get("Content-Type"),
		"REMOTE_ADDR":           remoteHost(r.RemoteAddr),
		"HTTP_GIT_PROTOCOL":     r.Header.Get("Git-Protocol"),
		"GIT_PROTOCOL":          r.Header.Get("Git-Protocol"),
		"CONTINUITY_REQUEST_ID": env.RequestID,
		"CONTINUITY_PUSH_ID":    env.PushID,
		"CONTINUITY_REPO_ID":    env.RepoID,
		"CONTINUITY_REPO_NAME":  env.RepoName,
		"CONTINUITY_NODE_ID":    env.NodeID,
		"CONTINUITY_DATA_DIR":   env.DataDir,
		"CONTINUITY_LOCK_HELD":  "1",
	}
	if r.ContentLength >= 0 {
		values["CONTENT_LENGTH"] = strconv.FormatInt(r.ContentLength, 10)
	}
	command.Env = overlayEnvironment(os.Environ(), values)
	command.Stdin = r.Body
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	headers, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("parse git-http-backend headers: %w: %s", err, stderr.String())
	}
	status := http.StatusOK
	if value := headers.Get("Status"); value != "" {
		fields := strings.Fields(value)
		parsed, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return fmt.Errorf("invalid CGI status %q", value)
		}
		status = parsed
		headers.Del("Status")
	}
	for key, list := range headers {
		for _, value := range list {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(status)
	if _, err := io.Copy(w, reader); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("git-http-backend: %w: %s", err, stderr.String())
	}
	return nil
}

func overlayEnvironment(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := values[key]; !replaced {
			result = append(result, item)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
func remoteHost(address string) string {
	host, _, ok := strings.Cut(address, ":")
	if ok {
		return host
	}
	return address
}
