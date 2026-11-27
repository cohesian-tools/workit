// Package void quarantines directory trees with a durable recovery record.
package void

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/cohesian-tools/workit/internal/workdir"
	"github.com/cohesian-tools/workit/internal/workspace"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type Vault struct{}

const marker = "workit-void-v1\n"

func record(file string, data []byte) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func directory(root string, create bool) (string, error) {
	base := filepath.Join(root, ".void")
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(base, 0700); err != nil {
			return "", err
		}
		if err := record(filepath.Join(base, ".workit-void"), []byte(marker)); err != nil {
			return "", err
		}
		return base, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf(".void must be a real directory")
	}
	file := filepath.Join(base, ".workit-void")
	info, err = os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing an uninitialized .void directory")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != marker {
		return "", fmt.Errorf("refusing an unrecognized .void directory")
	}
	return base, nil
}

func (Vault) Move(root string, node graph.Node, index []byte) (workspace.Receipt, error) {
	var receipt workspace.Receipt
	if node.Path == "." {
		return receipt, fmt.Errorf("cannot quarantine the root")
	}
	original, exists, err := workdir.Resolve(root, node.Path)
	if err != nil {
		return receipt, err
	}
	if !exists {
		return receipt, os.ErrNotExist
	}
	base, err := directory(root, true)
	if err != nil {
		return receipt, err
	}
	entry, err := os.MkdirTemp(base, time.Now().UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return receipt, err
	}
	payload := filepath.Join(entry, "payload", filepath.FromSlash(node.Path))
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(entry)
		}
	}()
	data, err := json.MarshalIndent(struct {
		Version int        `json:"version"`
		Node    graph.Node `json:"node"`
		Created time.Time  `json:"created"`
	}{1, node, time.Now().UTC()}, "", "  ")
	if err != nil {
		return receipt, err
	}
	if err := record(filepath.Join(entry, "entry.json"), append(data, '\n')); err != nil {
		return receipt, err
	}
	if err := record(filepath.Join(entry, "index-before.toml"), index); err != nil {
		return receipt, err
	}
	if err := os.MkdirAll(filepath.Dir(payload), 0700); err != nil {
		return receipt, err
	}
	if err := os.Rename(original, payload); err != nil {
		return receipt, err
	}
	cleanup = false
	return workspace.Receipt{Original: original, Payload: payload, Entry: entry}, nil
}

func (Vault) Restore(root string, receipt workspace.Receipt) error {
	if _, err := os.Lstat(receipt.Original); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot restore into an occupied path; payload remains at %s", receipt.Payload)
	}
	relative, err := filepath.Rel(root, receipt.Original)
	if err != nil {
		return err
	}
	if _, _, err := workdir.Resolve(root, filepath.ToSlash(relative)); err != nil {
		return err
	}
	if err := os.Rename(receipt.Payload, receipt.Original); err != nil {
		return fmt.Errorf("payload remains at %s: %w", receipt.Payload, err)
	}
	return os.RemoveAll(receipt.Entry)
}

func (Vault) Entries(root string) ([]string, error) {
	base, err := directory(root, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if entry.Name() == ".workit-void" {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("unrecognized void entry %s; cleanup refused", entry.Name())
		}
		file := filepath.Join(base, entry.Name(), "entry.json")
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("invalid recovery record at %s", file)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var record struct {
			Version int        `json:"version"`
			Node    graph.Node `json:"node"`
		}
		if err := json.Unmarshal(data, &record); err != nil || record.Version != 1 || !graph.ValidPath(record.Node.Path) || record.Node.Path == "." {
			return nil, fmt.Errorf("invalid recovery record at %s", file)
		}
		result = append(result, entry.Name())
	}
	return result, nil
}

func (v Vault) Clean(root string, expected []string) error {
	actual, err := v.Entries(root)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("void changed after planning; cleanup refused")
	}
	for _, entry := range actual {
		if err := os.RemoveAll(filepath.Join(root, ".void", entry)); err != nil {
			return err
		}
	}
	return nil
}
