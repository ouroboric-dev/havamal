package havamal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// EvalFile and ResultFile are an own skill's eval and its recorded result, in its folder. Both are
// files of the skill, so the lock pins them.
const (
	EvalFile   = "eval.yaml"
	ResultFile = "eval-result.json"
	EvalSchema = "ygg.skill-eval/v1"
	// ResultSchema names the format of ResultFile.
	ResultSchema = "ygg.skill-eval-result/v1"
)

// Eval is an own skill's eval: tasks an agent does with the skill and without it, and checks that
// say whether it did them right. Each trial passes only when every check of its task passes.
type Eval struct {
	Schema  string     `yaml:"schema"`
	Runs    int        `yaml:"runs"`     // trials per task and arm
	MinGain float64    `yaml:"min_gain"` // the pass rate with the skill must beat the rate without by this much
	Tasks   []EvalTask `yaml:"tasks"`
}

// EvalTask is one task.
type EvalTask struct {
	ID     string            `yaml:"id"`
	Prompt string            `yaml:"prompt"`
	Files  map[string]string `yaml:"files"` // the working directory at the start: path to content
	Checks []Check           `yaml:"checks"`
}

// Check is one deterministic check of a trial. Exactly one field is set.
type Check struct {
	FileExists        string    `yaml:"file_exists"`
	FileContains      *PathText `yaml:"file_contains"`
	FileNotContains   *PathText `yaml:"file_not_contains"`
	FileMatches       *PathText `yaml:"file_matches"` // Text is a regular expression
	OutputContains    string    `yaml:"output_contains"`
	OutputNotContains string    `yaml:"output_not_contains"`
}

// PathText names a file and a text.
type PathText struct {
	Path string `yaml:"path"`
	Text string `yaml:"text"`
}

// ParseEval reads an eval exactly, and refuses one that can't tell a skill that helps from one that
// doesn't: no tasks, a task with no check, too few runs, or a gain of zero.
func ParseEval(data []byte) (*Eval, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var e Eval
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("%s: %w", EvalFile, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document", EvalFile)
	}
	switch {
	case e.Schema != EvalSchema:
		return nil, fmt.Errorf("%s: schema %q, want %q", EvalFile, e.Schema, EvalSchema)
	case e.Runs < 3:
		return nil, fmt.Errorf("%s: runs is %d; a model's answers vary, so each task needs at least 3 trials per arm", EvalFile, e.Runs)
	case e.MinGain <= 0 || e.MinGain > 1:
		return nil, fmt.Errorf("%s: min_gain %v must be above 0 and at most 1", EvalFile, e.MinGain)
	case len(e.Tasks) == 0:
		return nil, fmt.Errorf("%s: no tasks", EvalFile)
	}
	seen := map[string]bool{}
	for _, t := range e.Tasks {
		switch {
		case !namePattern.MatchString(t.ID) || seen[t.ID]:
			return nil, fmt.Errorf("%s: task id %q is not a unique name", EvalFile, t.ID)
		case strings.TrimSpace(t.Prompt) == "":
			return nil, fmt.Errorf("%s: task %s has no prompt", EvalFile, t.ID)
		case len(t.Checks) == 0:
			return nil, fmt.Errorf("%s: task %s has no check, so every answer would pass", EvalFile, t.ID)
		}
		seen[t.ID] = true
		for p := range t.Files {
			if !filepath.IsLocal(p) {
				return nil, fmt.Errorf("%s: task %s: file %q must be a path inside the working directory", EvalFile, t.ID, p)
			}
		}
		for i, c := range t.Checks {
			if err := c.validate(); err != nil {
				return nil, fmt.Errorf("%s: task %s, check %d: %w", EvalFile, t.ID, i+1, err)
			}
		}
	}
	return &e, nil
}

func (c Check) validate() error {
	n := 0
	for _, set := range []bool{c.FileExists != "", c.FileContains != nil, c.FileNotContains != nil, c.FileMatches != nil, c.OutputContains != "", c.OutputNotContains != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		return errors.New("set exactly one field")
	}
	for _, p := range []string{c.FileExists} {
		if p != "" && !filepath.IsLocal(p) {
			return fmt.Errorf("path %q must be inside the working directory", p)
		}
	}
	for _, pt := range []*PathText{c.FileContains, c.FileNotContains, c.FileMatches} {
		if pt != nil && (!filepath.IsLocal(pt.Path) || pt.Text == "") {
			return errors.New("a file check needs a path inside the working directory and a text")
		}
	}
	if c.FileMatches != nil {
		if _, err := regexp.Compile(c.FileMatches.Text); err != nil {
			return err
		}
	}
	return nil
}

// grade runs the checks of a task on a finished trial.
func (t EvalTask) grade(workdir, output string) bool {
	read := func(p string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(workdir, p))
		return string(b), err == nil
	}
	for _, c := range t.Checks {
		ok := false
		switch {
		case c.FileExists != "":
			_, ok = read(c.FileExists)
		case c.FileContains != nil:
			s, found := read(c.FileContains.Path)
			ok = found && strings.Contains(s, c.FileContains.Text)
		case c.FileNotContains != nil:
			s, found := read(c.FileNotContains.Path)
			ok = found && !strings.Contains(s, c.FileNotContains.Text)
		case c.FileMatches != nil:
			s, found := read(c.FileMatches.Path)
			ok = found && regexp.MustCompile(c.FileMatches.Text).MatchString(s)
		case c.OutputContains != "":
			ok = strings.Contains(output, c.OutputContains)
		case c.OutputNotContains != "":
			ok = !strings.Contains(output, c.OutputNotContains)
		}
		if !ok {
			return false
		}
	}
	return true
}

// Trial is one run of one task, in a fresh working directory. Skill is nil for the arm without the
// skill: the same task, the same agent, nothing else different.
type Trial struct {
	Task    EvalTask
	Workdir string
	Skill   *Loaded
}

// EvalAgent runs a trial and returns what the agent said. A failure to run is an error, and it
// stops the eval: a trial that couldn't run is not a failed answer.
type EvalAgent interface {
	Run(ctx context.Context, t Trial) (output string, err error)
}

// Arm counts the trials of one arm.
type Arm struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

func (a Arm) rate() float64 {
	if a.Total == 0 {
		return 0
	}
	return float64(a.Passed) / float64(a.Total)
}

// Result is what an eval showed. It names the skill's content hash, so a changed skill needs a new
// eval.
type Result struct {
	Schema      string  `json:"schema"`
	Skill       string  `json:"skill"`
	SkillSHA256 string  `json:"skill_sha256"`
	Model       string  `json:"model"`
	Date        string  `json:"date"`
	Runs        int     `json:"runs"`
	MinGain     float64 `json:"min_gain"`
	Control     Arm     `json:"control"`   // without the skill
	Treatment   Arm     `json:"treatment"` // with it
	Gain        float64 `json:"gain"`
	Passed      bool    `json:"passed"`
}

// ContentHash is the hash of a skill's files, apart from its recorded result.
func ContentHash(e Entry) string {
	e.Files = slices.DeleteFunc(slices.Clone(e.Files), func(f File) bool { return f.Path == ResultFile })
	return SetHash([]Entry{e})
}

// RunEval runs every task of an eval Runs times in each arm, in a fresh directory each, and counts
// the trials whose checks all pass. Arms alternate within a task, so a drifting model or a slow hour
// can't favour one arm.
func RunEval(ctx context.Context, skill Loaded, e *Eval, agent EvalAgent, model, date string) (Result, error) {
	r := Result{Schema: ResultSchema, Skill: skill.Name, SkillSHA256: ContentHash(skill.Entry), Model: model, Date: date, Runs: e.Runs, MinGain: e.MinGain}
	for _, t := range e.Tasks {
		for i := 0; i < e.Runs; i++ {
			for _, with := range []bool{false, true} {
				ok, err := trial(ctx, t, skill, with, agent)
				if err != nil {
					return Result{}, fmt.Errorf("task %s: %w", t.ID, err)
				}
				arm := &r.Control
				if with {
					arm = &r.Treatment
				}
				arm.Total++
				if ok {
					arm.Passed++
				}
			}
		}
	}
	r.Gain = r.Treatment.rate() - r.Control.rate()
	r.Passed = r.Gain >= e.MinGain
	return r, nil
}

func trial(ctx context.Context, t EvalTask, skill Loaded, with bool, agent EvalAgent) (bool, error) {
	dir, err := os.MkdirTemp("", "havamal-eval-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	for p, text := range t.Files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			return false, err
		}
	}
	tr := Trial{Task: t, Workdir: dir}
	if with {
		tr.Skill = &skill
	}
	out, err := agent.Run(ctx, tr)
	if err != nil {
		return false, err
	}
	return t.grade(dir, out), nil
}

// CheckEvals checks that every own skill ships with an eval and a passing, current result: the
// result names this skill's content hash, passed, and shows the gain the eval asked for (spec §5.1).
// It returns one line per problem. It reads the files, so a hand-edited result is no cheaper than a
// real one: the lock pins the result file, and a person reviews the diff.
func CheckEvals(fsys fs.FS) ([]string, error) {
	raw, err := fs.ReadFile(fsys, LockFile)
	if err != nil {
		return nil, err
	}
	lock, err := ParseLock(raw)
	if err != nil {
		return []string{err.Error()}, nil
	}
	var problems []string
	for _, e := range lock.Skills {
		if !e.Own {
			continue
		}
		say := func(f string, a ...any) { problems = append(problems, "skill "+e.Name+": "+fmt.Sprintf(f, a...)) }
		raw, err := fs.ReadFile(fsys, path.Join(SkillsDir, e.Name, EvalFile))
		if err != nil {
			say("our own skills ship with an eval, and %s is missing", EvalFile)
			continue
		}
		ev, err := ParseEval(raw)
		if err != nil {
			say("%v", err)
			continue
		}
		raw, err = fs.ReadFile(fsys, path.Join(SkillsDir, e.Name, ResultFile))
		if err != nil {
			say("no %s: run the eval and commit its result", ResultFile)
			continue
		}
		var r Result
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		switch {
		case dec.Decode(&r) != nil || r.Schema != ResultSchema || r.Skill != e.Name:
			say("%s is not a result for this skill", ResultFile)
		case r.SkillSHA256 != ContentHash(e):
			say("%s is for an older version of the skill; run the eval again", ResultFile)
		case r.Gain < ev.MinGain || !r.Passed || r.MinGain != ev.MinGain || r.Runs != ev.Runs:
			say("the eval does not show the skill helps: gain %.2f, the eval asks for %.2f", r.Gain, ev.MinGain)
		case r.Control.Total != r.Treatment.Total || r.Control.Total != ev.Runs*len(ev.Tasks):
			say("%s doesn't count every trial of the eval", ResultFile)
		}
	}
	return problems, nil
}
