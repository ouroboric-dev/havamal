package havamal

import (
	"strings"
	"testing"
)

// The factory's reviewers load the simplicity review of Ponytail (spec §9.5 rung 6, spec-driven
// development §8). It is vendored, not installed: a pinned, reviewed copy of one commit.
func TestThePonytailReviewIsVendoredAtAPinnedCommitUnderItsLicence(t *testing.T) {
	lib, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() = %v; the library has drifted from its lock", err)
	}
	got, err := lib.Get("ponytail-review")
	if err != nil {
		t.Fatalf("Get(ponytail-review) = %v", err)
	}
	e := got.Entry
	if e.Own || e.Licence != "MIT" || e.Upstream == nil || e.Upstream.Repo != "https://github.com/DietrichGebert/ponytail" ||
		e.Upstream.Path != "skills/ponytail-review" || e.Upstream.Commit != "16352ac229eb08a9598dcae48fe9f2b342567d86" {
		t.Errorf("entry = %+v; want the upstream's public URL at one full commit, under MIT", e)
	}
	var paths []string
	for _, f := range e.Files {
		paths = append(paths, f.Path)
	}
	if !strings.Contains(strings.Join(paths, " "), "LICENSE") {
		t.Errorf("the vendored files %v lack the upstream's licence", paths)
	}
	if !strings.Contains(got.Body, "Lists findings, changes no code.") {
		t.Error("the skill's text is not the one that was reviewed")
	}
}
