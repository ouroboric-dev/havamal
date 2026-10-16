package havamal

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Skill is what a skill's SKILL.md says about it, and its text.
type Skill struct {
	Name        string
	Description string
	Body        string // SKILL.md after its front matter
}

// ParseSkill reads a SKILL.md: a YAML front matter with a name and a description, then the text.
// The folder name must be the skill's name, so a skill can't pose as another.
func ParseSkill(folder string, data []byte) (Skill, error) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return Skill{}, errors.New("SKILL.md has no front matter")
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		if head, ok = bytes.CutSuffix(rest, []byte("\n---")); !ok {
			return Skill{}, errors.New("SKILL.md's front matter has no closing ---")
		}
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(head, &fm); err != nil {
		return Skill{}, fmt.Errorf("SKILL.md front matter: %w", err)
	}
	switch {
	case fm.Name != folder:
		return Skill{}, fmt.Errorf("SKILL.md names the skill %q, but its folder is %q", fm.Name, folder)
	case strings.TrimSpace(fm.Description) == "":
		return Skill{}, errors.New("SKILL.md has no description")
	}
	return Skill{Name: fm.Name, Description: fm.Description, Body: strings.TrimLeft(string(body), "\n")}, nil
}
