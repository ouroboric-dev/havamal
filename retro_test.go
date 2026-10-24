package havamal

import (
	"strings"
	"testing"
)

// The factory's retrospective node loads Matt Pocock's retro skill (typed-boundaries design). It is
// vendored, not installed: a pinned, reviewed copy of one commit.
func TestTheRetroSkillIsVendoredAtAPinnedCommitUnderItsLicence(t *testing.T) {
	lib, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() = %v; the library has drifted from its lock", err)
	}
	got, err := lib.Get("retro")
	if err != nil {
		t.Fatalf("Get(retro) = %v", err)
	}
	e := got.Entry
	if e.Own || e.Licence != "MIT" || e.Upstream == nil || e.Upstream.Repo != "https://github.com/mattpocock/skills" ||
		e.Upstream.Path != "skills/engineering/retro" || e.Upstream.Commit != "5beb35b040e3de97e54fa9e386d7e42d15c295c2" {
		t.Errorf("entry = %+v; want the upstream's public URL at one full commit, under MIT", e)
	}
	var paths []string
	for _, f := range e.Files {
		paths = append(paths, f.Path)
	}
	if !strings.Contains(strings.Join(paths, " "), "LICENSE") {
		t.Errorf("the vendored files %v lack the upstream's licence", paths)
	}
	if !strings.Contains(got.Body, "suggesting improvements to the coding agent's **environment**") {
		t.Error("the skill's text is not the one that was reviewed")
	}
}
