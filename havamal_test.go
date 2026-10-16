package havamal

import (
	"strings"
	"testing"
	"testing/fstest"
)

const skillMD = "---\nname: asd-ste100\ndescription: Write one meaning per sentence.\n---\n# ASD-STE100\n\nUse short sentences.\n"

// library builds a small library in memory, with the lock that matches it.
func library(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{
		"skills/.gitkeep":            {},
		"skills/asd-ste100/SKILL.md": {Data: []byte(skillMD)},
		"skills/asd-ste100/LICENSE":  {Data: []byte("MIT licence text\n")},
		"skills/ours/SKILL.md":       {Data: []byte("---\nname: ours\ndescription: Our own skill.\n---\nDo it well.\n")},
	}
	lock := &Lock{Skills: []Entry{
		{Name: "asd-ste100", Licence: "MIT", Upstream: &Upstream{Repo: "https://github.com/example/writing-skills", Path: "skills/asd-ste100", Commit: strings.Repeat("a", 40)}, Files: files(fsys, "asd-ste100", "LICENSE", "SKILL.md")},
		{Name: "ours", Own: true, Licence: "AGPL-3.0-only", Files: files(fsys, "ours", "SKILL.md")},
	}}
	b, err := lock.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	fsys["skills.lock"] = &fstest.MapFile{Data: b}
	return fsys
}

func files(fsys fstest.MapFS, skill string, names ...string) []File {
	var fs []File
	for _, n := range names {
		fs = append(fs, File{Path: n, SHA256: Sum(fsys["skills/"+skill+"/"+n].Data)})
	}
	return fs
}

func TestParseLockRefusesWhatItCannotReadExactly(t *testing.T) {
	good := string(library(t)["skills.lock"].Data)
	bad := map[string]string{
		"an unknown field":                strings.Replace(good, `"schema"`, `"extra": 1, "schema"`, 1),
		"a second document":               good + good,
		"a licence we can't redistribute": strings.Replace(good, `"licence": "MIT"`, `"licence": "CC-BY-NC-4.0"`, 1),
		"a short commit":                  strings.Replace(good, strings.Repeat("a", 40), "abc123", 1),
		"an upstream that isn't https":    strings.Replace(good, "https://github.com", "http://github.com", 1),
		"a short hash":                    strings.Replace(good, Sum([]byte("MIT licence text\n")), "abc", 1),
		"a wrong schema":                  strings.Replace(good, LockSchema, "ygg.skills-lock/v9", 1),
	}
	for name, text := range bad {
		if _, err := ParseLock([]byte(text)); err == nil {
			t.Errorf("%s: ParseLock() = nil, want an error", name)
		}
	}
}

func TestSkillMustCarryItsOwnName(t *testing.T) {
	if _, err := ParseSkill("asd-ste100", []byte(skillMD)); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"no front matter": "# hi\n",
		"no description":  "---\nname: asd-ste100\n---\nhi\n",
		"another name":    "---\nname: x\ndescription: d\n---\nhi\n",
	} {
		if _, err := ParseSkill("asd-ste100", []byte(text)); err == nil {
			t.Errorf("%s: ParseSkill() = nil, want an error", name)
		}
	}
}

func TestSetHashChangesWithAnyFile(t *testing.T) {
	l, _ := ParseLock(library(t)["skills.lock"].Data)
	a := SetHash(l.Skills)
	l.Skills[0].Files[0].SHA256 = Sum([]byte("changed"))
	if b := SetHash(l.Skills); a == b || len(a) != 64 {
		t.Errorf("the set hash didn't change: %s %s", a, b)
	}
}
