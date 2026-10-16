package havamal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
)

// Library is a set of skills Gleipnir may load. The embedded library is the one the factory ships,
// pinned by the version of this module; an operator's private directory is another.
type Library struct {
	fsys    fs.FS
	lock    *Lock
	private bool
}

// Loaded is a skill picked for a task.
type Loaded struct {
	Skill
	Entry   Entry
	Private bool // from the operator's own directory, not the pinned library
}

// Open opens a library and refuses it if it has drifted from its lock: a skill is instructions an
// agent follows with full permissions, so nothing unreviewed loads (spec §14, third-party skills).
func Open(fsys fs.FS) (*Library, error) {
	drift, err := Verify(fsys)
	if err != nil {
		return nil, err
	}
	if len(drift) > 0 {
		return nil, fmt.Errorf("the skill library has drifted from %s:\n  %s", LockFile, joinLines(drift))
	}
	raw, err := fs.ReadFile(fsys, LockFile)
	if err != nil {
		return nil, err
	}
	lock, err := ParseLock(raw)
	if err != nil {
		return nil, err
	}
	return &Library{fsys: fsys, lock: lock}, nil
}

// OpenPrivate opens an operator's directory of private skills: one folder each, with a SKILL.md.
// It has no lock, so nothing here is pinned; the entries record each file's hash, and run.json
// keeps them (spec §5.1).
func OpenPrivate(dir string) (*Library, error) {
	fsys := os.DirFS(dir)
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	l := &Library{fsys: privateRoot{fsys}, lock: &Lock{Schema: LockSchema, Skills: []Entry{}}, private: true}
	for _, d := range dirs {
		if !d.IsDir() || d.Name()[0] == '.' {
			continue
		}
		e := Entry{Name: d.Name(), Own: true, Licence: "private"}
		err := fs.WalkDir(fsys, d.Name(), func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return err
			}
			if !de.Type().IsRegular() {
				return fmt.Errorf("private skill %s: %s is not a regular file", d.Name(), p)
			}
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			rel, _ := relTo(d.Name(), p)
			e.Files = append(e.Files, File{Path: rel, SHA256: Sum(b)})
			return nil
		})
		if err != nil {
			return nil, err
		}
		b, err := fs.ReadFile(fsys, path.Join(d.Name(), "SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("private skill %s: %w", d.Name(), err)
		}
		if _, err := ParseSkill(d.Name(), b); err != nil {
			return nil, fmt.Errorf("private skill %s: %w", d.Name(), err)
		}
		sort.Slice(e.Files, func(i, j int) bool { return e.Files[i].Path < e.Files[j].Path })
		l.lock.Skills = append(l.lock.Skills, e)
	}
	return l, nil
}

// privateRoot lets a private directory answer to the same paths as the pinned library, under skills/.
type privateRoot struct{ fs.FS }

func (p privateRoot) Open(name string) (fs.File, error) {
	if name == SkillsDir {
		return p.FS.Open(".")
	}
	if rest, ok := cut(name, SkillsDir+"/"); ok {
		return p.FS.Open(rest)
	}
	return nil, fs.ErrNotExist
}

func cut(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func relTo(root, p string) (string, bool) { return cut(p, root+"/") }

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}

// Names returns the skills in the library, sorted.
func (l *Library) Names() []string {
	var names []string
	for _, e := range l.lock.Skills {
		names = append(names, e.Name)
	}
	return names
}

// Get returns one skill.
func (l *Library) Get(name string) (Loaded, error) {
	e, ok := l.lock.Entry(name)
	if !ok {
		return Loaded{}, fmt.Errorf("no skill %q", name)
	}
	b, err := fs.ReadFile(l.fsys, path.Join(SkillsDir, name, "SKILL.md"))
	if err != nil {
		return Loaded{}, err
	}
	s, err := ParseSkill(name, b)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{Skill: s, Entry: e, Private: l.private}, nil
}

// Select picks the named skills, in the order named. Each name must be in exactly one library: an
// operator's private skill never shadows a pinned one, and no skill loads that a task didn't name
// (spec §5.1: skills load explicitly).
func Select(names []string, libs ...*Library) ([]Loaded, error) {
	var out []Loaded
	for i, name := range names {
		if slices.Contains(names[:i], name) {
			return nil, fmt.Errorf("skill %q is named twice", name)
		}
		var found []Loaded
		for _, l := range libs {
			if l == nil {
				continue
			}
			if !slices.Contains(l.Names(), name) {
				continue
			}
			s, err := l.Get(name)
			if err != nil {
				return nil, err
			}
			found = append(found, s)
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("no skill %q in the library; vendor it first (spec §5.1)", name)
		case 1:
			out = append(out, found[0])
		default:
			return nil, errors.New("skill " + name + " is in the pinned library and in a private directory; rename the private one")
		}
	}
	return out, nil
}

// Entries returns the lock entries of the loaded skills.
func Entries(loaded []Loaded) []Entry {
	es := make([]Entry, len(loaded))
	for i, l := range loaded {
		es[i] = l.Entry
	}
	return es
}
