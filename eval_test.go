package havamal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const evalYAML = `schema: ygg.skill-eval/v1
runs: 3
min_gain: 0.5
tasks:
  - id: sign-off
    prompt: Write report.md that ends with a sign-off.
    files: { notes.md: "draft" }
    checks:
      - file_exists: report.md
      - file_contains: { path: report.md, text: "Yours, the factory" }
      - output_not_contains: "I cannot"
`

// obeys is an agent that follows the skill when it is loaded, and not otherwise.
type obeys struct{ calls int }

func (o *obeys) Run(_ context.Context, t Trial) (string, error) {
	o.calls++
	text := "report"
	if t.Skill != nil && strings.Contains(t.Skill.Body, "sign off") {
		text += "\nYours, the factory"
	}
	return "done", os.WriteFile(filepath.Join(t.Workdir, "report.md"), []byte(text), 0o644)
}

func ownSkill(t *testing.T) Loaded {
	t.Helper()
	fsys := fstest.MapFS{
		"skills/signer/SKILL.md":  {Data: []byte("---\nname: signer\ndescription: Sign reports.\n---\nAlways sign off.\n")},
		"skills/signer/eval.yaml": {Data: []byte(evalYAML)},
	}
	lock := &Lock{Skills: []Entry{{Name: "signer", Own: true, Licence: "AGPL-3.0-only", Files: []File{
		{Path: "SKILL.md", SHA256: Sum(fsys["skills/signer/SKILL.md"].Data)}, {Path: "eval.yaml", SHA256: Sum([]byte(evalYAML))}}}}}
	b, _ := lock.Marshal()
	fsys["skills.lock"] = &fstest.MapFile{Data: b}
	fsys["skills/.gitkeep"] = &fstest.MapFile{}
	lib, err := Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	s, err := lib.Get("signer")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEvalShowsAHelpfulSkillHelps(t *testing.T) {
	e, err := ParseEval([]byte(evalYAML))
	if err != nil {
		t.Fatal(err)
	}
	agent := &obeys{}
	r, err := RunEval(context.Background(), ownSkill(t), e, agent, "test-model", "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	if agent.calls != 6 || r.Control != (Arm{0, 3}) || r.Treatment != (Arm{3, 3}) || r.Gain != 1 || !r.Passed {
		t.Errorf("result = %+v after %d trials; want 0/3 without, 3/3 with, gain 1", r, agent.calls)
	}
}

type ignores struct{}

func (ignores) Run(_ context.Context, t Trial) (string, error) {
	return "done", os.WriteFile(filepath.Join(t.Workdir, "report.md"), []byte("report"), 0o644)
}

func TestEvalFailsASkillThatChangesNothing(t *testing.T) {
	e, _ := ParseEval([]byte(evalYAML))
	r, err := RunEval(context.Background(), ownSkill(t), e, ignores{}, "m", "d")
	if err != nil || r.Passed || r.Gain != 0 {
		t.Errorf("result = %+v, %v; a skill that changes nothing must not pass", r, err)
	}
}

func TestParseEvalRefusesAnEvalThatCannotTell(t *testing.T) {
	for name, text := range map[string]string{
		"two runs":              strings.Replace(evalYAML, "runs: 3", "runs: 2", 1),
		"no gain asked":         strings.Replace(evalYAML, "min_gain: 0.5", "min_gain: 0", 1),
		"a task, no check":      strings.Replace(evalYAML, evalYAML[strings.Index(evalYAML, "    checks:"):], "    checks: []\n", 1),
		"two fields in a check": strings.Replace(evalYAML, "- file_exists: report.md", "- file_exists: report.md\n        output_contains: x", 1),
		"a path that climbs":    strings.Replace(evalYAML, "file_exists: report.md", "file_exists: ../report.md", 1),
		"an unknown field":      evalYAML + "extra: 1\n",
		"a bad regexp":          strings.Replace(evalYAML, "- file_exists: report.md", "- file_matches: { path: report.md, text: \"(\" }", 1),
	} {
		if _, err := ParseEval([]byte(text)); err == nil {
			t.Errorf("%s: ParseEval() = nil, want an error", name)
		}
	}
}

func TestCheckEvalsNeedsACurrentPassingResult(t *testing.T) {
	skill := ownSkill(t)
	build := func(result string) fstest.MapFS {
		files := []File{{Path: "SKILL.md", SHA256: skill.Entry.Files[0].SHA256}, {Path: "eval.yaml", SHA256: skill.Entry.Files[1].SHA256}}
		fsys := fstest.MapFS{
			"skills/.gitkeep":         {},
			"skills/signer/SKILL.md":  {Data: []byte("---\nname: signer\ndescription: Sign reports.\n---\nAlways sign off.\n")},
			"skills/signer/eval.yaml": {Data: []byte(evalYAML)},
		}
		if result != "" {
			fsys["skills/signer/eval-result.json"] = &fstest.MapFile{Data: []byte(result)}
			files = append(files, File{Path: ResultFile, SHA256: Sum([]byte(result))})
		}
		b, _ := (&Lock{Skills: []Entry{{Name: "signer", Own: true, Licence: "AGPL-3.0-only", Files: files}}}).Marshal()
		fsys["skills.lock"] = &fstest.MapFile{Data: b}
		return fsys
	}
	good := `{"schema":"ygg.skill-eval-result/v1","skill":"signer","skill_sha256":"` + ContentHash(skill.Entry) + `","model":"m","date":"d","runs":3,"min_gain":0.5,"control":{"passed":0,"total":3},"treatment":{"passed":3,"total":3},"gain":1,"passed":true}`
	if p, err := CheckEvals(build(good)); err != nil || len(p) != 0 {
		t.Errorf("a passing result: %q, %v", p, err)
	}
	for name, c := range map[string]struct{ result, want string }{
		"no result":       {"", "no eval-result.json"},
		"a stale result":  {strings.Replace(good, ContentHash(skill.Entry), strings.Repeat("0", 64), 1), "older version"},
		"too little gain": {strings.Replace(strings.Replace(good, `"gain":1`, `"gain":0.1`, 1), `"passed":true`, `"passed":false`, 1), "does not show the skill helps"},
		"a result claiming a pass with a low gain": {strings.Replace(good, `"gain":1`, `"gain":0.1`, 1), "does not show the skill helps"},
	} {
		p, err := CheckEvals(build(c.result))
		if err != nil || !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: %q, %v; want a line with %q", name, p, err, c.want)
		}
	}
}

func TestRelockRecordsAnOwnSkillAndRefusesAVendoredOne(t *testing.T) {
	lib := emptyLibrary(t)
	skillDir := filepath.Join(lib, "skills", "signer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: signer\ndescription: Sign reports.\n---\nAlways sign off.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Relock(lib, "signer", ""); err == nil {
		t.Error("a new skill without a licence was locked")
	}
	if err := Relock(lib, "signer", "AGPL-3.0-only"); err != nil {
		t.Fatal(err)
	}
	if drift, _ := Verify(os.DirFS(lib)); len(drift) != 0 {
		t.Errorf("drift after relock: %q", drift)
	}
	problems, _ := CheckEvals(os.DirFS(lib))
	if !strings.Contains(strings.Join(problems, "\n"), "ship with an eval") {
		t.Errorf("an own skill without an eval passed the check: %q", problems)
	}
	src, commit := upstreamRepo(t, nil)
	if err := Vendor(context.Background(), options(lib, src, commit)); err != nil {
		t.Fatal(err)
	}
	if err := Relock(lib, "plain-language", ""); err == nil {
		t.Error("Relock changed a vendored skill")
	}
}
