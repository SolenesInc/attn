package automode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	attngit "github.com/victorarias/attn/internal/git"
)

const RepositoryRulesFile = ".attn/rules.json"

type RepositoryRules struct {
	Path  string
	Rules []Rule
}

type repositoryRulesDocument struct {
	Rules []Rule `json:"rules"`
}

func LoadRepositoryRules(cwd string) (RepositoryRules, error) {
	root, err := attngit.GetRepoRoot(cwd)
	if err != nil {
		present, markerErr := repositoryMarkerPresent(cwd)
		if markerErr != nil {
			return RepositoryRules{}, fmt.Errorf("discover repository auto-mode rules from %s: %w", cwd, markerErr)
		}
		if present {
			return RepositoryRules{}, fmt.Errorf("discover repository auto-mode rules from %s: %w", cwd, err)
		}
		return RepositoryRules{Rules: []Rule{}}, nil
	}
	if root == "" {
		return RepositoryRules{Rules: []Rule{}}, nil
	}
	path := filepath.Join(root, RepositoryRulesFile)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return RepositoryRules{Rules: []Rule{}}, nil
	}
	if err != nil {
		return RepositoryRules{}, fmt.Errorf("read repository auto-mode rules %s: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var document repositoryRulesDocument
	if err := decoder.Decode(&document); err != nil {
		return RepositoryRules{}, fmt.Errorf("read repository auto-mode rules %s: %w", path, err)
	}
	if err := expectRepositoryRulesEOF(decoder); err != nil {
		return RepositoryRules{}, fmt.Errorf("read repository auto-mode rules %s: %w", path, err)
	}
	if document.Rules == nil {
		document.Rules = []Rule{}
	}
	for i := range document.Rules {
		document.Rules[i] = NormalizeRule(document.Rules[i])
		if err := ValidateRule(document.Rules[i]); err != nil {
			return RepositoryRules{}, fmt.Errorf("read repository auto-mode rules %s: rule %d: %w", path, i+1, err)
		}
		if err := validateRepositoryRuleExamples(document.Rules[i]); err != nil {
			return RepositoryRules{}, fmt.Errorf("read repository auto-mode rules %s: rule %d: %w", path, i+1, err)
		}
	}
	return RepositoryRules{Path: path, Rules: document.Rules}, nil
}

func repositoryMarkerPresent(cwd string) (bool, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return false, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("working directory is not a directory: %s", dir)
	}
	for {
		_, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

func validateRepositoryRuleExamples(rule Rule) error {
	for _, example := range rule.NotMatch {
		if len(example) == 0 {
			return fmt.Errorf("not_match example cannot be empty")
		}
		if repositoryRuleMatches(rule, example) {
			return fmt.Errorf("not_match example %q matches %q", example, rule.Describe())
		}
	}
	for _, example := range rule.Match {
		if len(example) == 0 {
			return fmt.Errorf("match example cannot be empty")
		}
		if !repositoryRuleMatches(rule, example) {
			return fmt.Errorf("match example %q does not match %q", example, rule.Describe())
		}
	}
	return nil
}

func repositoryRuleMatches(rule Rule, command []string) bool {
	if len(command) < len(rule.Pattern) {
		return false
	}
	for i, token := range rule.Pattern {
		word := command[i]
		matched := false
		for _, alternative := range token.Alternatives {
			if alternative == word || (i == 0 && filepath.Base(filepath.Clean(word)) == alternative) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func expectRepositoryRulesEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("the file contains more than one JSON value")
}

func MergeRepositoryRules(cfg Config, repository RepositoryRules) Config {
	cfg.Rules = append(append([]Rule{}, cfg.Rules...), repository.Rules...)
	return cfg
}
