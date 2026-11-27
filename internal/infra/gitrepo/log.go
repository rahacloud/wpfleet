package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// LogEntry is one commit in the history of a file.
type LogEntry struct {
	// SHA is the commit's full hash.
	SHA string
	// Author is the author's name: for a promotion from the UI, the person
	// who made it.
	Author string
	// Email is the author's email.
	Email string
	// When is the author's time.
	When time.Time
	// Subject is the first line of the message.
	Subject string
}

// FileLog returns the newest n commits at or before the head that changed
// path (absolute, or relative to the clone), newest first. A path no commit
// touched gives an empty list.
func (r *Repo) FileLog(ctx context.Context, path string, n int) ([]LogEntry, error) {
	rel, err := r.relative(path)
	if err != nil {
		return nil, err
	}

	name := filepath.ToSlash(rel)

	r.mu.RLock()
	defer r.mu.RUnlock()

	//nolint:exhaustruct_v5 // no date range, no ordering override: the default log from the head
	iter, err := r.repo.Log(&git.LogOptions{From: r.head, FileName: &name})
	if err != nil {
		return nil, fmt.Errorf("reading the history of %s: %w", name, err)
	}
	defer iter.Close()

	entries := make([]LogEntry, 0, min(max(n, 0), 64)) //nolint:mnd // a small first capacity; the UI asks for a page

	err = iter.ForEach(func(c *object.Commit) error {
		if len(entries) >= n {
			return storer.ErrStop
		}

		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("reading the history: %w", err)
		}

		subject, _, _ := strings.Cut(c.Message, "\n")
		entries = append(entries, LogEntry{
			SHA:     c.Hash.String(),
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			When:    c.Author.When,
			Subject: subject,
		})

		return nil
	})
	if err != nil && !errors.Is(err, storer.ErrStop) {
		return nil, fmt.Errorf("reading the history of %s: %w", name, err)
	}

	return entries, nil
}
