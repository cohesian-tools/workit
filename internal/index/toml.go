// Package index persists the versioned root catalog. It implements workspace.Store.
package index

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
)

const Filename = ".workit.toml"

type Store struct{}

func (Store) Find(directory string) (string, error) {
	current, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Lstat(filepath.Join(current, Filename))
		if err == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s must be a regular file", Filename)
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		current = parent
	}
}

func (Store) Load(root string) (graph.Index, []byte, error) {
	var index graph.Index
	file := filepath.Join(root, Filename)
	info, err := os.Lstat(file)
	if err != nil {
		return index, nil, err
	}
	if !info.Mode().IsRegular() {
		return index, nil, fmt.Errorf("index must be a regular file")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return index, nil, err
	}
	if err := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields().Decode(&index); err != nil {
		return index, nil, fmt.Errorf("index TOML: %w", err)
	}
	if err := index.Validate(); err != nil {
		return index, nil, err
	}
	return index, raw, nil
}

func (Store) Create(root string, index graph.Index) error {
	if err := index.Validate(); err != nil {
		return err
	}
	raw, err := toml.Marshal(index)
	if err != nil {
		return err
	}
	temp, err := prepare(root, raw, 0644)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	return os.Link(temp, filepath.Join(root, Filename))
}

func (Store) Save(root string, index graph.Index, expected []byte) error {
	if err := index.Validate(); err != nil {
		return err
	}
	graph.Sort(index.Nodes)
	raw, err := toml.Marshal(index)
	if err != nil {
		return err
	}
	file := filepath.Join(root, Filename)
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("index must be a regular file")
	}
	current, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("index changed after planning; no index write performed")
	}
	temp, err := prepare(root, raw, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	info, err = os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("index path changed while preparing write")
	}
	current, err = os.ReadFile(file)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("index changed while preparing write")
	}
	return os.Rename(temp, file)
}

func prepare(root string, data []byte, mode os.FileMode) (string, error) {
	temp, err := os.CreateTemp(root, ".workit-index-*")
	if err != nil {
		return "", err
	}
	file := temp.Name()
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		_ = os.Remove(file)
		return "", err
	}
	return file, nil
}

func (Store) Lock(root string) (func(), error) {
	lock := filepath.Join(root, ".workit.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return nil, fmt.Errorf("workspace busy or stale lock at %s: %w", lock, err)
	}
	return func() { _ = os.Remove(lock) }, nil
}
