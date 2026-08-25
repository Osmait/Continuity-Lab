package procreceive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/githooks/pktline"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/continuity-lab/continuity-lab/internal/wal"
)

func Run(ctx context.Context, input io.Reader, output io.Writer, stderr io.Writer, cfg config.Config, store objectstore.ObjectStore) error {
	reader := pktline.NewReader(input)
	versionLine, err := reader.Read()
	if err != nil {
		return err
	}
	version, offered, err := parseVersion(versionLine)
	if err != nil || version != "version=1" {
		return errors.New("proc-receive requires protocol version 1")
	}
	if _, err := reader.Read(); !errors.Is(err, pktline.ErrFlush) {
		return errors.New("proc-receive negotiation lacks flush packet")
	}
	selected := make([]string, 0, 2)
	if offered["push-options"] {
		selected = append(selected, "push-options")
	}
	if offered["atomic"] {
		selected = append(selected, "atomic")
	}
	response := []byte("version=1")
	if len(selected) > 0 {
		response = append(response, 0)
		response = append(response, []byte(strings.Join(selected, " "))...)
	}
	response = append(response, '\n')
	if err := pktline.Write(output, response); err != nil {
		return err
	}
	if err := pktline.Flush(output); err != nil {
		return err
	}

	commands, err := readCommands(reader, cfg.MaxRefsPerPush)
	if err != nil {
		return err
	}
	if offered["push-options"] {
		for {
			_, err := reader.Read()
			if errors.Is(err, pktline.ErrFlush) {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	pushID, repoID := os.Getenv("CONTINUITY_PUSH_ID"), os.Getenv("CONTINUITY_REPO_ID")
	manager := &repository.Manager{Config: cfg, Store: store}
	var pending model.Pending
	pendingErr := repository.ReadJSON(manager.PendingPath(repoID, pushID), &pending)
	if pendingErr != nil || pending.PushID != pushID || pending.RepoID != repoID || pending.State != "payload_durable" || time.Since(pending.CreatedAt) > cfg.PendingTTL || !sameCommands(pending.Updates, commands) {
		reason := "durable pre-receive state does not match proc-receive commands"
		if pendingErr != nil {
			fmt.Fprintf(stderr, "continuity: pending state: %v\n", pendingErr)
		}
		return report(output, commands, errors.New(reason))
	}
	committer := wal.Committer{Config: cfg, Store: store, Manager: manager}
	receipt, err := committer.CommitPush(ctx, pending.Updates, pending)
	if err != nil {
		fmt.Fprintf(stderr, "continuity: push %s rejected or uncertain: %v\n", pushID, err)
		return report(output, commands, err)
	}
	fmt.Fprintf(stderr, "continuity: committed sequence %d\n", receipt.Sequence)
	return report(output, commands, nil)
}

func parseVersion(payload []byte) (string, map[string]bool, error) {
	payload = []byte(strings.TrimSuffix(string(payload), "\n"))
	parts := strings.SplitN(string(payload), "\x00", 2)
	features := map[string]bool{}
	if len(parts) == 2 {
		for _, feature := range strings.Fields(parts[1]) {
			features[feature] = true
		}
	}
	if parts[0] == "" {
		return "", nil, errors.New("empty proc-receive version")
	}
	return parts[0], features, nil
}

func readCommands(reader *pktline.Reader, max int) ([]model.RefUpdate, error) {
	commands := make([]model.RefUpdate, 0)
	seen := map[string]bool{}
	for {
		payload, err := reader.Read()
		if errors.Is(err, pktline.ErrFlush) {
			break
		}
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(strings.TrimSuffix(string(payload), "\n"))
		if len(fields) != 3 {
			return nil, errors.New("invalid proc-receive command")
		}
		update := model.RefUpdate{OldOID: fields[0], NewOID: fields[1], Ref: fields[2]}
		if err := update.Validate(); err != nil {
			return nil, err
		}
		if seen[update.Ref] {
			return nil, errors.New("duplicate proc-receive ref")
		}
		seen[update.Ref] = true
		commands = append(commands, update)
		if len(commands) > max {
			return nil, errors.New("too many proc-receive commands")
		}
	}
	if len(commands) == 0 {
		return nil, errors.New("empty proc-receive command set")
	}
	return commands, nil
}

func sameCommands(pending, commands []model.RefUpdate) bool {
	if len(pending) != len(commands) {
		return false
	}
	for i := range pending {
		if pending[i].Ref != commands[i].Ref || pending[i].OldOID != commands[i].OldOID || pending[i].NewOID != commands[i].NewOID {
			return false
		}
	}
	return true
}

func report(output io.Writer, commands []model.RefUpdate, failure error) error {
	for _, command := range commands {
		line := "ok " + command.Ref + "\n"
		if failure != nil {
			line = "ng " + command.Ref + " " + sanitize(failure.Error()) + "\n"
		}
		if err := pktline.Write(output, []byte(line)); err != nil {
			return err
		}
	}
	return pktline.Flush(output)
}

func sanitize(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}
