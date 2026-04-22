package recipe

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading recipe %q: %w", path, err)
	}

	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing recipe %q: %w", path, err)
	}

	if err := validate(&r); err != nil {
		return nil, fmt.Errorf("invalid recipe %q: %w", path, err)
	}

	return &r, nil
}

func validate(r *Recipe) error {
	validActions := map[string]bool{
		"add-file":        true,
		"add-dir":         true,
		"remove-file":     true,
		"remove-dir":      true,
		"replace-file":    true,
		"copy-file-from":  true,
		"extract-file":    true,
		"unzip-to":        true,
		"use-base":        true,
		"set-env":         true,
		"set-label":       true,
		"set-entrypoint":  true,
		"set-cmd":         true,
		"set-user":        true,
		"set-workdir":     true,
		"shadow":          true,
		"fix-permissions": true,
	}

	for i, step := range r.Steps {
		if !validActions[step.Action] {
			return fmt.Errorf("step %d: unknown action %q", i, step.Action)
		}
	}

	return nil
}
