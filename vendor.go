package havamal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// VendorOptions say which skill to vendor, from where.
type VendorOptions struct {
	Dir     string // the library: holds skills/ and skills.lock
	Name    string // the skill's name, which is its folder's name
	Repo    string // the upstream's public https URL, as the lock records it
	Source  string // where git fetches from; empty means Repo. Tests point it at a local repo.
	Commit  string // the full commit hash to vendor
	Path    string // the skill's folder in the upstream
	Licence string // an SPDX id from Redistributable
	Update  bool   // replace a skill that is already vendored
}

const maxVendorBytes = 5 << 20

// Vendor copies one skill from an upstream repository, at one commit, into the library and records
// it in skills.lock. It reads the commit's objects and never checks anything out, so no filter,
// hook or symlink in the upstream runs or lands. The change is a diff for a person to review (spec
// §5.1: updates are reviewed pull requests).
func Vendor(ctx context.Context, o VendorOptions) error {
	switch {
	case !namePattern.MatchString(o.Name):
		return fmt.Errorf("%q is not a skill name", o.Name)
	case !strings.HasPrefix(o.Repo, "https://"):
		return fmt.Errorf("upstream %q must be a public https URL; never vendor from a copy synced through a plugin system", o.Repo)
	case !commitPattern.MatchString(o.Commit):
		return fmt.Errorf("commit %q must be a full 40-digit hash, not a branch or tag that can move", o.Commit)
	case !validPath(o.Path):
		return fmt.Errorf("path %q is not a folder inside the upstream", o.Path)
	case !slices.Contains(Redistributable, o.Licence):
		return fmt.Errorf("licence %q is not one the factory may redistribute (%s)", o.Licence, strings.Join(Redistributable, ", "))
	}
	lockPath := filepath.Join(o.Dir, LockFile)
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	lock, err := ParseLock(raw)
	if err != nil {
		return err
	}
	dest := filepath.Join(o.Dir, SkillsDir, o.Name)
	_, have := lock.Entry(o.Name)
	if _, err := os.Stat(dest); (err == nil || have) && !o.Update {
		return fmt.Errorf("skill %s is already in the library; pass update to replace it", o.Name)
	}

	g, err := newUpstream(ctx, firstNonEmpty(o.Source, o.Repo), o.Commit)
	if err != nil {
		return err
	}
	defer g.close()
	files, err := g.files(ctx, o.Path)
	if err != nil {
		return err
	}
	if _, ok := files["SKILL.md"]; !ok {
		return fmt.Errorf("%s has no SKILL.md at %s", o.Repo, o.Path)
	}
	if _, err := ParseSkill(o.Name, files["SKILL.md"]); err != nil {
		return err
	}
	if !slices.ContainsFunc(mapKeys(files), isLicenceFile) {
		lic, err := g.rootLicence(ctx)
		if err != nil {
			return fmt.Errorf("the skill folder has no licence file and %w", err)
		}
		files["LICENSE"] = lic
	}

	entry := Entry{Name: o.Name, Licence: o.Licence, Upstream: &Upstream{Repo: o.Repo, Path: o.Path, Commit: o.Commit}}
	for p, b := range files {
		entry.Files = append(entry.Files, File{Path: p, SHA256: Sum(b)})
	}
	if have {
		lock.Skills = slices.DeleteFunc(lock.Skills, func(e Entry) bool { return e.Name == o.Name })
	}
	lock.Skills = append(lock.Skills, entry)
	out, err := lock.Marshal()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	for p, b := range files {
		full := filepath.Join(dest, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(lockPath, out, 0o644)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mapKeys(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// upstream is a bare repository holding one fetched commit.
type upstream struct {
	dir, commit string
}

func newUpstream(ctx context.Context, source, commit string) (*upstream, error) {
	dir, err := os.MkdirTemp("", "havamal-vendor-")
	if err != nil {
		return nil, err
	}
	u := &upstream{dir: dir, commit: commit}
	if _, err := u.git(ctx, "init", "-q", "--bare"); err != nil {
		u.close()
		return nil, err
	}
	if _, err := u.git(ctx, "fetch", "-q", "--depth=1", "--no-tags", source, commit); err != nil {
		u.close()
		return nil, err
	}
	if got, err := u.git(ctx, "rev-parse", "--verify", "FETCH_HEAD^{commit}"); err != nil || strings.TrimSpace(string(got)) != commit {
		u.close()
		return nil, fmt.Errorf("%s did not give commit %s", source, commit)
	}
	return u, nil
}

func (u *upstream) close() { os.RemoveAll(u.dir) }

// git runs git with hooks, the operator's config and every transport but https and file off.
func (u *upstream) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always", "-c", "protocol.file.allow=always",
	}, args...)...)
	cmd.Dir = u.dir
	cmd.Env = append(cleanEnv(), "GIT_DIR="+u.dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.Bytes(), nil
}

func cleanEnv() []string {
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			e = append(e, kv)
		}
	}
	return e
}

// files returns every file under dir in the commit, by path inside dir. A symlink, a submodule or
// more than maxVendorBytes is refused.
func (u *upstream) files(ctx context.Context, dir string) (map[string][]byte, error) {
	list, err := u.git(ctx, "ls-tree", "-r", "-z", "--full-tree", "FETCH_HEAD", "--", dir)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	total := 0
	for _, rec := range strings.Split(string(list), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unreadable tree entry %q", rec)
		}
		if f[0] != "100644" && f[0] != "100755" {
			return nil, fmt.Errorf("%s is not a plain file (mode %s); a skill holds no symlinks or submodules", p, f[0])
		}
		blob, err := u.git(ctx, "cat-file", "blob", f[2])
		if err != nil {
			return nil, err
		}
		if total += len(blob); total > maxVendorBytes {
			return nil, errors.New("the skill folder is over 5 MiB; vendor a skill, not a repository")
		}
		files[strings.TrimPrefix(p, dir+"/")] = blob
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files at %s in the upstream", dir)
	}
	return files, nil
}

func (u *upstream) rootLicence(ctx context.Context) ([]byte, error) {
	list, err := u.git(ctx, "ls-tree", "-z", "--name-only", "FETCH_HEAD")
	if err != nil {
		return nil, err
	}
	for _, n := range strings.Split(string(list), "\x00") {
		if n != "" && isLicenceFile(path.Base(n)) {
			return u.git(ctx, "cat-file", "blob", "FETCH_HEAD:"+n)
		}
	}
	return nil, errors.New("the upstream has no licence file at its root")
}
