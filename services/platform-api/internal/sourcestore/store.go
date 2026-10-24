// Package sourcestore keeps every build's source archive, exactly as it was
// uploaded, so the approver can review the files that were built before
// signing (the platform owner, 2026-10-24: "review every file before it goes
// up"). One file per build, under one directory per application, readable
// only by platform-api; an application's directory goes when it is deleted.
package sourcestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// Store is a directory of <application id>/<build id>.tar.gz.
type Store struct {
	root string
}

func New(root string) *Store { return &Store{root: root} }

// IDs are UUIDs; anything else would be a path someone constructed.
var safeID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (s *Store) path(applicationID, buildID string) (string, error) {
	if !safeID.MatchString(applicationID) || (buildID != "" && !safeID.MatchString(buildID)) {
		return "", fmt.Errorf("sourcestore: invalid id")
	}
	if buildID == "" {
		return filepath.Join(s.root, applicationID), nil
	}
	return filepath.Join(s.root, applicationID, buildID+".tar.gz"), nil
}

// Save writes a build's archive, replacing nothing: a build id is new.
func (s *Store) Save(applicationID, buildID string, archive []byte) error {
	p, err := s.path(applicationID, buildID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("sourcestore: %w", err)
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, archive, 0o600); err != nil {
		return fmt.Errorf("sourcestore: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sourcestore: %w", err)
	}
	return nil
}

// Open returns a build's archive and its size.
func (s *Store) Open(applicationID, buildID string) (io.ReadCloser, int64, error) {
	p, err := s.path(applicationID, buildID)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// PurgeApplication deletes every kept source of an application.
func (s *Store) PurgeApplication(_ context.Context, applicationID string) error {
	p, err := s.path(applicationID, "")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sourcestore: %w", err)
	}
	return nil
}
