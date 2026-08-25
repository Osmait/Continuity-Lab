package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
)

var client = &http.Client{Timeout: 10 * time.Minute}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(raw []string) error {
	args, jsonOutput := removeFlag(raw, "--json")
	if len(args) == 2 && args[0] == "storage" && args[1] == "conformance" {
		return storageConformance()
	}
	cfg, err := config.Load("continuityctl")
	if err != nil {
		return err
	}
	if len(args) == 2 && args[0] == "cluster" && args[1] == "status" {
		return requestAndPrint(http.MethodGet, cfg.GatewayURL+"/api/v1/cluster", nil, jsonOutput)
	}
	if len(args) >= 2 && args[0] == "repo" {
		switch args[1] {
		case "create":
			if len(args) != 3 {
				return usage()
			}
			body, _ := json.Marshal(map[string]string{"name": args[2], "default_branch": "main"})
			return requestAndPrint(http.MethodPost, cfg.GatewayURL+"/api/v1/repos", body, jsonOutput)
		case "inspect", "refs", "wal":
			if len(args) < 3 {
				return usage()
			}
			suffix := ""
			if args[1] != "inspect" {
				suffix = "/" + args[1]
			}
			if args[1] == "wal" {
				if limit := option(args[3:], "--limit"); limit != "" {
					suffix += "?limit=" + limit
				}
			}
			return requestAndPrint(http.MethodGet, cfg.GatewayURL+"/api/v1/repos/"+args[2]+suffix, nil, jsonOutput)
		case "verify":
			if len(args) < 3 {
				return usage()
			}
			_, allNodes := removeFlag(args[3:], "--all-nodes")
			if allNodes {
				return verifyAllNodes(args[2], jsonOutput)
			}
			return requestAndPrint(http.MethodPost, cfg.GatewayURL+"/api/v1/repos/"+args[2]+"/verify", nil, jsonOutput)
		case "compact":
			if len(args) < 3 {
				return usage()
			}
			return requestAndPrint(http.MethodPost, cfg.GatewayURL+"/api/v1/repos/"+args[2]+"/compact", nil, jsonOutput)
		case "gc":
			if len(args) < 3 {
				return usage()
			}
			_, dry := removeFlag(args[3:], "--dry-run")
			return requestAndPrint(http.MethodPost, cfg.GatewayURL+"/api/v1/repos/"+args[2]+"/gc?dry_run="+strconv.FormatBool(dry), nil, jsonOutput)
		case "evict":
			if len(args) < 5 || args[3] != "--node" {
				return usage()
			}
			return requestAndPrint(http.MethodPost, nodeURL(args[4])+"/api/v1/repos/"+args[2]+"/evict", nil, jsonOutput)
		}
	}
	if len(args) == 5 && args[0] == "node" && args[1] == "cache" && args[2] == "list" && args[3] == "--node" {
		return requestAndPrint(http.MethodGet, nodeURL(args[4])+"/api/v1/cache", nil, jsonOutput)
	}
	if len(args) >= 4 && args[0] == "failpoint" {
		switch args[1] {
		case "set":
			node := option(args[3:], "--node")
			if node == "" {
				return usage()
			}
			return requestAndPrint(http.MethodPut, nodeURL(node)+"/api/v1/failpoints/"+args[2], []byte(`{"mode":"once"}`), jsonOutput)
		case "clear":
			node := option(args[3:], "--node")
			if node == "" {
				return usage()
			}
			return requestAndPrint(http.MethodDelete, nodeURL(node)+"/api/v1/failpoints/"+args[2], nil, jsonOutput)
		}
	}
	return usage()
}

func storageConformance() error {
	cfg, err := config.Load("continuityctl")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := objectstore.NewS3(ctx, cfg)
	if err != nil {
		return err
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return err
	}
	if err := objectstore.Conformance(ctx, store); err != nil {
		return err
	}
	fmt.Println("storage conformance: PASS")
	return nil
}

func verifyAllNodes(name string, raw bool) error {
	results := make([]map[string]any, 0, 3)
	for _, node := range []string{"node-a", "node-b", "node-c"} {
		payload, err := requestPayload(http.MethodPost, nodeURL(node)+"/api/v1/repos/"+name+"/verify", nil)
		if err != nil {
			return fmt.Errorf("verify %s: %w", node, err)
		}
		var result any
		if err := json.Unmarshal(payload, &result); err != nil {
			return fmt.Errorf("decode verify %s: %w", node, err)
		}
		results = append(results, map[string]any{"node": node, "result": result})
	}
	var output []byte
	if raw {
		output, _ = json.Marshal(results)
	} else {
		output, _ = json.MarshalIndent(results, "", "  ")
	}
	fmt.Println(string(output))
	return nil
}

func requestAndPrint(method, endpoint string, body []byte, raw bool) error {
	payload, err := requestPayload(method, endpoint, body)
	if err != nil {
		return err
	}
	if raw {
		_, err = os.Stdout.Write(payload)
		if len(payload) > 0 && payload[len(payload)-1] != '\n' {
			fmt.Println()
		}
		return err
	}
	var value any
	if json.Unmarshal(payload, &value) == nil {
		pretty, _ := json.MarshalIndent(value, "", "  ")
		fmt.Println(string(pretty))
		return nil
	}
	fmt.Print(string(payload))
	return nil
}

func requestPayload(method, endpoint string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func nodeURL(id string) string {
	if value := os.Getenv("CONTINUITY_NODE_URL_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))); value != "" {
		return strings.TrimRight(value, "/")
	}
	ports := map[string]int{"node-a": 18081, "node-b": 18082, "node-c": 18083}
	return "http://localhost:" + strconv.Itoa(ports[id])
}
func removeFlag(args []string, name string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == name {
			found = true
		} else {
			out = append(out, arg)
		}
	}
	return out, found
}
func option(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
func usage() error {
	return errors.New("usage: continuityctl storage conformance | cluster status | repo create|inspect|refs|wal|verify|compact|gc|evict ... | node cache list --node ID | failpoint set|clear ...")
}
