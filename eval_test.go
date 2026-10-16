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
