package havamal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// upstreamRepo makes a git repo with a skill in it, and returns it and its commit.
func upstreamRepo(t *testing.T, extra map[string]string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	files := map[string]string{
		"LICENSE":                        "MIT licence text\n",
		"skills/plain-language/SKILL.md": "---\nname: plain-language\ndescription: Write plainly.\n---\nUse short words.\n",
		"skills/plain-language/ref.md":   "A reference.\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for p, text := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) string {
		c := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.invalid"}, args...)...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "skills")
	return dir, run("rev-parse", "HEAD")
}

func emptyLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills.lock"), []byte(`{"schema":"ygg.skills-lock/v1","skills":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func options(lib, src, commit string) VendorOptions {
	return VendorOptions{Dir: lib, Name: "plain-language", Repo: "https://github.com/example/writing-skills", Source: src,
		Commit: commit, Path: "skills/plain-language", Licence: "MIT"}
}

func TestVendorCopiesTheSkillAndLocksIt(t *testing.T) {
	src, commit := upstreamRepo(t, nil)
	lib := emptyLibrary(t)
	if err := Vendor(context.Background(), options(lib, src, commit)); err != nil {
		t.Fatal(err)
	}
	drift, err := Verify(os.DirFS(lib))
	if err != nil || len(drift) != 0 {
		t.Fatalf("the vendored library has drift: %q, %v", drift, err)
	}
	b, _ := os.ReadFile(filepath.Join(lib, "skills.lock"))
	l, _ := ParseLock(b)
	e, _ := l.Entry("plain-language")
	if e.Upstream.Commit != commit || e.Upstream.Path != "skills/plain-language" || len(e.Files) != 3 {
		t.Errorf("entry = %+v; want the commit, the path, and three files (the licence came from the upstream root)", e)
	}
	if lic, _ := os.ReadFile(filepath.Join(lib, "skills/plain-language/LICENSE")); string(lic) != "MIT licence text\n" {
		t.Errorf("licence file = %q", lic)
	}
	if err := Vendor(context.Background(), options(lib, src, commit)); err == nil {
		t.Error("vendoring a skill twice without update succeeded")
	}
	o := options(lib, src, commit)
	o.Update = true
	if err := Vendor(context.Background(), o); err != nil {
		t.Errorf("update: %v", err)
	}
}

func TestVendorRefusesWhatItShouldNotCopy(t *testing.T) {
	src, commit := upstreamRepo(t, nil)
	lib := emptyLibrary(t)
	bad := map[string]func(*VendorOptions){
		"a branch name":                func(o *VendorOptions) { o.Commit = "main" },
		"an unredistributable licence": func(o *VendorOptions) { o.Licence = "CC-BY-NC-4.0" },
		"a non-https upstream":         func(o *VendorOptions) { o.Repo = "git@github.com:example/w.git" },
		"a path that climbs":           func(o *VendorOptions) { o.Path = "../x" },
		"another skill's name":         func(o *VendorOptions) { o.Name = "orwells-rules" },
		"a commit the upstream lacks":  func(o *VendorOptions) { o.Commit = strings.Repeat("b", 40) },
	}
	for name, change := range bad {
		o := options(lib, src, commit)
		change(&o)
		if err := Vendor(context.Background(), o); err == nil {
			t.Errorf("%s: Vendor() = nil, want an error", name)
		}
	}
	branch := options(lib, src, commit)
	branch.Commit = "main"
	if err := Vendor(context.Background(), branch); err == nil || !strings.Contains(err.Error(), "full 40-digit") {
		t.Errorf("a branch name: %v, want a refusal that says a full commit is needed", err)
	}
	// A symlink in the skill folder is refused, and nothing lands.
	src2, commit2 := upstreamRepo(t, nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(src2, "skills/plain-language/link")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "link"}, {"rev-parse", "HEAD"}} {
		c := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.invalid"}, args...)...)
		c.Dir = src2
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		commit2 = strings.TrimSpace(string(out))
	}
	if err := Vendor(context.Background(), options(lib, src2, commit2)); err == nil || !strings.Contains(err.Error(), "plain file") {
		t.Errorf("a symlink in the skill: %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "skills/plain-language")); err == nil {
		t.Error("a refused vendor left files behind")
	}
}
