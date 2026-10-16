// Package havamal is the skill library (spec §5.1): vendored skills pinned by skills.lock, our own
// skills, and the checks that keep them honest. It depends on nothing of the factory.
package havamal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// LockSchema names the skills.lock format.
const LockSchema = "ygg.skills-lock/v1"

// Lock is skills.lock: what each skill is, where it came from, and a hash of each of its files.
type Lock struct {
	Schema string  `json:"schema"`
	Skills []Entry `json:"skills"`
}

// Entry pins one skill.
type Entry struct {
	Name     string    `json:"name"`
	Own      bool      `json:"own,omitempty"`      // written here, not vendored; it ships with an eval
	Upstream *Upstream `json:"upstream,omitempty"` // set for every vendored skill
	Licence  string    `json:"licence"`            // an SPDX id from Redistributable
	Files    []File    `json:"files"`              // sorted by path
}

// Upstream says where a vendored skill came from, to the commit.
type Upstream struct {
	Repo   string `json:"repo"`   // the public repository's https URL
	Path   string `json:"path"`   // the skill's folder inside it
	Commit string `json:"commit"` // the full commit hash
}

// File is one file of a skill, by path inside the skill's folder.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Redistributable lists the licences a skill may carry: the factory vendors nothing it can't
// redistribute (spec §5.1).
var Redistributable = []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "CC0-1.0", "CC-BY-4.0", "Unlicense", "AGPL-3.0-only"}

var (
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sumPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseLock reads skills.lock exactly: an unknown field, a second document or an entry that breaks
// a rule is an error, and nothing is defaulted.
func ParseLock(data []byte) (*Lock, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var l Lock
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("skills.lock: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("skills.lock: more than one JSON document")
	}
	if err := l.check(); err != nil {
		return nil, fmt.Errorf("skills.lock: %w", err)
	}
	return &l, nil
}

func (l *Lock) check() error {
	if l.Schema != LockSchema {
		return fmt.Errorf("schema %q, want %q", l.Schema, LockSchema)
	}
	var prev string
	for i, e := range l.Skills {
		switch {
		case !namePattern.MatchString(e.Name):
			return fmt.Errorf("skill %q is not a skill name", e.Name)
		case i > 0 && e.Name <= prev:
			return fmt.Errorf("skill %q is not in sorted order, or is listed twice", e.Name)
		case !slices.Contains(Redistributable, e.Licence):
			return fmt.Errorf("skill %s: licence %q is not one the factory may redistribute (%s)", e.Name, e.Licence, strings.Join(Redistributable, ", "))
		case e.Own && e.Upstream != nil:
			return fmt.Errorf("skill %s is our own, so it has no upstream", e.Name)
		case !e.Own && e.Upstream == nil:
			return fmt.Errorf("skill %s is vendored, so it needs an upstream", e.Name)
		case len(e.Files) == 0:
			return fmt.Errorf("skill %s lists no files", e.Name)
		}
		prev = e.Name
		if u := e.Upstream; u != nil {
			if !strings.HasPrefix(u.Repo, "https://") || !commitPattern.MatchString(u.Commit) || u.Path == "" {
				return fmt.Errorf("skill %s: upstream needs an https repo, a path and a full commit hash", e.Name)
			}
		}
		for j, f := range e.Files {
			switch {
			case !validPath(f.Path):
				return fmt.Errorf("skill %s: file %q is not a path inside the skill", e.Name, f.Path)
			case j > 0 && f.Path <= e.Files[j-1].Path:
				return fmt.Errorf("skill %s: file %q is not in sorted order, or is listed twice", e.Name, f.Path)
			case !sumPattern.MatchString(f.SHA256):
				return fmt.Errorf("skill %s: file %s has no SHA-256", e.Name, f.Path)
			}
		}
	}
	return nil
}

func validPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "\\") && slices.Index(strings.Split(p, "/"), "..") < 0 &&
		slices.Index(strings.Split(p, "/"), ".") < 0 && !strings.HasSuffix(p, "/")
}

// Marshal writes the lock as ParseLock reads it: sorted, indented, one trailing newline.
func (l *Lock) Marshal() ([]byte, error) {
	sort.Slice(l.Skills, func(i, j int) bool { return l.Skills[i].Name < l.Skills[j].Name })
	for i := range l.Skills {
		fs := l.Skills[i].Files
		sort.Slice(fs, func(a, b int) bool { return fs[a].Path < fs[b].Path })
	}
	l.Schema = LockSchema
	if err := l.check(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	return append(b, '\n'), err
}

// Entry returns the entry for name.
func (l *Lock) Entry(name string) (Entry, bool) {
	for _, e := range l.Skills {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Sum returns the SHA-256 of data, in hex.
func Sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// SetHash is the hash of a set of skills: one line per file, "<skill> <path> <sha256>", sorted. It
// goes into run.json (spec §5.1), so a result says which skills the run used.
func SetHash(entries []Entry) string {
	var lines []string
	for _, e := range entries {
		for _, f := range e.Files {
			lines = append(lines, e.Name+" "+f.Path+" "+f.SHA256)
		}
	}
	sort.Strings(lines)
	return Sum([]byte(strings.Join(lines, "\n") + "\n"))
}
