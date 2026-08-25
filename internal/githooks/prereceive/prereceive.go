package prereceive

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/repository"
)

func Run(ctx context.Context, input io.Reader, stderr io.Writer, cfg config.Config, store objectstore.ObjectStore) error {
	pushID, repoID := os.Getenv("CONTINUITY_PUSH_ID"), os.Getenv("CONTINUITY_REPO_ID")
	if pushID == "" || len(repoID) != 64 {
		return errors.New("missing push identity")
	}
	updates, err := ReadCommands(input, cfg.MaxRefsPerPush)
	if err != nil {
		return err
	}
	markForcedUpdates(ctx, updates)
	if err := validateRepo(cfg, repoID); err != nil {
		return err
	}
	manager := &repository.Manager{Config: cfg, Store: store}
	if err := manager.PrepareDirs(); err != nil {
		return err
	}

	payload, err := buildAndUploadPack(ctx, cfg, store, repoID, updates)
	if err != nil {
		return err
	}
	if hit(cfg.DataDir, "after_payload_upload") {
		return errors.New("failpoint after_payload_upload")
	}
	pending := model.Pending{SchemaVersion: model.SchemaVersion, PushID: pushID, RepoID: repoID, NodeID: cfg.NodeID, CreatedAt: time.Now().UTC(), PID: os.Getpid(), State: "payload_durable", Payload: payload, Updates: updates}
	if err := repository.AtomicJSON(manager.PendingPath(repoID, pushID), pending, 0o640); err != nil {
		return fmt.Errorf("write pending push: %w", err)
	}
	fmt.Fprintf(stderr, "continuity: payload durable for push %s\n", pushID)
	return nil
}

func ReadCommands(input io.Reader, max int) ([]model.RefUpdate, error) {
	scanner := bufio.NewScanner(io.LimitReader(input, 4<<20))
	updates := make([]model.RefUpdate, 0)
	seen := map[string]bool{}
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), " ")
		if len(parts) != 3 {
			return nil, errors.New("invalid pre-receive command")
		}
		update := model.RefUpdate{OldOID: parts[0], NewOID: parts[1], Ref: parts[2]}
		if err := update.Validate(); err != nil {
			return nil, err
		}
		if seen[update.Ref] {
			return nil, errors.New("duplicate ref in push")
		}
		seen[update.Ref] = true
		updates = append(updates, update)
		if len(updates) > max {
			return nil, errors.New("push updates too many refs")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(updates) == 0 {
		return nil, errors.New("empty push command set")
	}
	return updates, nil
}

func markForcedUpdates(ctx context.Context, updates []model.RefUpdate) {
	for i := range updates {
		update := &updates[i]
		if update.OldOID == model.ZeroOID || update.NewOID == model.ZeroOID {
			continue
		}
		if !strings.HasPrefix(update.Ref, "refs/heads/") {
			update.Force = true
			continue
		}
		if exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", update.OldOID, update.NewOID).Run() != nil {
			update.Force = true
		}
	}
}

func buildAndUploadPack(ctx context.Context, cfg config.Config, store objectstore.ObjectStore, repoID string, updates []model.RefUpdate) (*model.PackPayload, error) {
	newOIDs := make([]string, 0, len(updates))
	for _, update := range updates {
		if update.NewOID != model.ZeroOID {
			newOIDs = append(newOIDs, update.NewOID)
		}
	}
	if len(newOIDs) == 0 {
		return nil, nil
	}
	args := append([]string{"rev-list", "--objects"}, newOIDs...)
	args = append(args, "--not", "--all")
	revList := exec.CommandContext(ctx, "git", args...)
	var objects bytes.Buffer
	revList.Stdout = &objects
	revList.Stderr = os.Stderr
	if err := revList.Run(); err != nil {
		return nil, fmt.Errorf("enumerate new objects: %w", err)
	}
	var objectIDs strings.Builder
	scanner := bufio.NewScanner(&objects)
	count := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && model.ValidOID(fields[0]) {
			objectIDs.WriteString(fields[0])
			objectIDs.WriteByte('\n')
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	pendingDir := filepath.Join(cfg.DataDir, "pending", repoID)
	if err := os.MkdirAll(pendingDir, 0o750); err != nil {
		return nil, err
	}
	packFile, err := os.CreateTemp(pendingDir, ".pack-*")
	if err != nil {
		return nil, err
	}
	packPath := packFile.Name()
	defer os.Remove(packPath)
	command := exec.CommandContext(ctx, "git", "pack-objects", "--stdout", "--delta-base-offset")
	command.Stdin = strings.NewReader(objectIDs.String())
	hash := sha256.New()
	command.Stdout = io.MultiWriter(packFile, hash)
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		_ = packFile.Close()
		return nil, fmt.Errorf("normalize push pack: %w", err)
	}
	if err := packFile.Sync(); err != nil {
		_ = packFile.Close()
		return nil, err
	}
	stat, err := packFile.Stat()
	if err != nil {
		_ = packFile.Close()
		return nil, err
	}
	if err := packFile.Close(); err != nil {
		return nil, err
	}
	sha := hex.EncodeToString(hash.Sum(nil))
	if err := validatePack(ctx, packPath); err != nil {
		return nil, err
	}
	readPack, err := os.Open(packPath)
	if err != nil {
		return nil, err
	}
	defer readPack.Close()
	key := model.PackKey(repoID, sha)
	if _, _, err := store.PutImmutable(ctx, key, readPack, stat.Size(), "application/x-git-packed-objects"); err != nil {
		return nil, fmt.Errorf("upload normalized pack: %w", err)
	}
	return &model.PackPayload{PackKey: key, PackSHA256: sha, PackSize: stat.Size()}, nil
}

func validatePack(ctx context.Context, path string) error {
	dir, err := os.MkdirTemp("", "continuity-pack-check-*.git")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	initCommand := exec.CommandContext(ctx, "git", "init", "--bare", dir)
	initCommand.Env = cleanValidationEnv(os.Environ())
	if output, err := initCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("init pack validator: %w: %s", err, output)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	command := exec.CommandContext(ctx, "git", "--git-dir="+dir, "index-pack", "--stdin", "--strict")
	command.Env = cleanValidationEnv(os.Environ())
	command.Stdin = file
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("validate normalized pack: %w: %s", err, output)
	}
	return nil
}

func cleanValidationEnv(environment []string) []string {
	blocked := map[string]bool{"GIT_DIR": true, "GIT_OBJECT_DIRECTORY": true, "GIT_QUARANTINE_PATH": true}
	clean := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[key] {
			clean = append(clean, item)
		}
	}
	return clean
}

func validateRepo(cfg config.Config, repoID string) error {
	gitDir, err := filepath.Abs(os.Getenv("GIT_DIR"))
	if err != nil {
		return err
	}
	expected, err := filepath.Abs(filepath.Join(cfg.DataDir, "repos", repoID+".git"))
	if err != nil {
		return err
	}
	if filepath.Clean(gitDir) != filepath.Clean(expected) {
		return fmt.Errorf("hook repository mismatch: %s", gitDir)
	}
	return nil
}

func hit(dataDir, name string) bool {
	value := os.Getenv("CONTINUITY_FAILPOINTS")
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	_, err := os.Stat(filepath.Join(dataDir, "failpoints", name))
	if err == nil {
		_ = os.Remove(filepath.Join(dataDir, "failpoints", name))
		return true
	}
	return false
}
