package havamal

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// SkillsDir is the folder that holds one folder per skill, and LockFile the lock beside it.
const (
	SkillsDir = "skills"
	LockFile  = "skills.lock"
)

// Verify checks a skill library against its lock, as the drift check of spec §5.1 does: every
// skill folder is in the lock and the other way round, every file is there with the hash the lock
// records, no file is extra, and every SKILL.md reads. It returns one line per problem, sorted,
// and nil when the library is as locked.
func Verify(fsys fs.FS) ([]string, error) {
	raw, err := fs.ReadFile(fsys, LockFile)
	if err != nil {
		return nil, err
	}
	lock, err := ParseLock(raw)
	if err != nil {
		return []string{err.Error()}, nil
	}
	var drift []string
	say := func(f string, a ...any) { drift = append(drift, fmt.Sprintf(f, a...)) }
	folders := map[string]bool{}
	dirs, err := fs.ReadDir(fsys, SkillsDir)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		if !d.IsDir() {
			say("%s/%s is a file; the library holds only skill folders", SkillsDir, d.Name())
			continue
		}
		folders[d.Name()] = true
		if _, ok := lock.Entry(d.Name()); !ok {
			say("skill %s is not in %s", d.Name(), LockFile)
		}
	}
	for _, e := range lock.Skills {
		if !folders[e.Name] {
			say("skill %s is in %s but has no folder", e.Name, LockFile)
			continue
		}
		drift = append(drift, verifySkill(fsys, e)...)
	}
	sort.Strings(drift)
	return drift, nil
}

func verifySkill(fsys fs.FS, e Entry) []string {
	var drift []string
	say := func(f string, a ...any) {
		drift = append(drift, fmt.Sprintf("skill %s: ", e.Name)+fmt.Sprintf(f, a...))
	}
	root := path.Join(SkillsDir, e.Name)
	want := map[string]string{}
	for _, f := range e.Files {
		want[f.Path] = f.SHA256
	}
	seen := map[string]bool{}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, root+"/")
		if !d.Type().IsRegular() {
			say("%s is not a regular file", rel)
			return nil
		}
		seen[rel] = true
		sum, ok := want[rel]
		if !ok {
			say("%s is not in the lock", rel)
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if Sum(b) != sum {
			say("%s differs from the lock", rel)
		}
		return nil
	})
	if err != nil {
		say("%v", err)
	}
	for p := range want {
		if !seen[p] {
			say("%s is in the lock but missing", p)
		}
	}
	if b, err := fs.ReadFile(fsys, path.Join(root, "SKILL.md")); err != nil {
		say("SKILL.md can't be read")
	} else if _, err := ParseSkill(e.Name, b); err != nil {
		say("%v", err)
	}
	if !e.Own && !slices.ContainsFunc(e.Files, func(f File) bool { return isLicenceFile(f.Path) }) {
		say("a vendored skill keeps its licence file, and the lock lists none")
	}
	return drift
}

func isLicenceFile(p string) bool {
	if strings.Contains(p, "/") {
		return false
	}
	u := strings.ToUpper(p)
	return strings.HasPrefix(u, "LICENSE") || strings.HasPrefix(u, "LICENCE") || strings.HasPrefix(u, "COPYING")
}
