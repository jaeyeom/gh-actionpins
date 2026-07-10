// Package scan inventories GitHub Actions references from local workflow files.
//
// It walks .github/workflows/** for *.yml / *.yaml files and extracts uses:
// values of the form owner/name@ref and owner/name/path@ref. Local actions
// (./...) and Docker images (docker://...) are ignored in v1.
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowsRel is the directory under a repo root that is scanned.
const WorkflowsRel = ".github/workflows"

// Finding is one uses: reference found in a workflow file.
type Finding struct {
	// File is the path relative to the scan root (slash-separated).
	File string `json:"file"`
	// Line is the 1-based line of the uses value in the source file.
	Line int `json:"line"`
	// Action is owner/name or owner/name/path (everything before @).
	Action string `json:"action"`
	// Ref is the pin after @ (tag, branch, or commit SHA).
	Ref string `json:"ref"`
	// Uses is the full uses string as written (action@ref).
	Uses string `json:"uses"`
}

// Result is the outcome of a scan.
type Result struct {
	// Root is the absolute path that was scanned.
	Root string `json:"root"`
	// Findings lists every remote action uses: in deterministic order.
	Findings []Finding `json:"findings"`
}

// Scan walks root/.github/workflows and returns action uses: findings.
// Missing workflows directory yields an empty result (not an error).
func Scan(root string) (*Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve scan root: %w", err)
	}

	res := &Result{Root: abs, Findings: []Finding{}}
	dir := filepath.Join(abs, WorkflowsRel)

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, fmt.Errorf("stat workflows dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workflows path is not a directory: %s", dir)
	}

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !isWorkflowFile(d.Name()) {
			return nil
		}
		findings, parseErr := parseFile(abs, path)
		if parseErr != nil {
			return parseErr
		}
		res.Findings = append(res.Findings, findings...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workflows: %w", err)
	}

	sortFindings(res.Findings)
	return res, nil
}

func isWorkflowFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

func parseFile(root, path string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("relative path for %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml %s: %w", rel, err)
	}

	var findings []Finding
	collectUses(&doc, rel, &findings)
	return findings, nil
}

// collectUses walks a yaml.Node tree and records remote action uses: values.
func collectUses(n *yaml.Node, file string, out *[]Finding) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			collectUses(c, file, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Value == "uses" && val.Kind == yaml.ScalarNode {
				if f, ok := findingFromUses(file, val.Line, val.Value); ok {
					*out = append(*out, f)
				}
				continue
			}
			collectUses(key, file, out)
			collectUses(val, file, out)
		}
	}
}

// findingFromUses parses a uses: scalar. Local and Docker refs are skipped.
func findingFromUses(file string, line int, raw string) (Finding, bool) {
	uses := strings.TrimSpace(raw)
	if uses == "" {
		return Finding{}, false
	}
	if shouldSkip(uses) {
		return Finding{}, false
	}

	action, ref, ok := splitActionRef(uses)
	if !ok {
		return Finding{}, false
	}
	return Finding{
		File:   file,
		Line:   line,
		Action: action,
		Ref:    ref,
		Uses:   uses,
	}, true
}

// shouldSkip reports whether uses is a local path or Docker image (v1 ignore).
func shouldSkip(uses string) bool {
	if strings.HasPrefix(uses, "docker://") {
		return true
	}
	// Local composite/reusable: relative path, optionally with @ref.
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") || strings.HasPrefix(uses, "/") {
		return true
	}
	return false
}

// splitActionRef splits owner/name[@/path]@ref into action and ref.
// Returns ok=false when the value is not a remote action reference.
func splitActionRef(uses string) (action, ref string, ok bool) {
	// owner/name@ref — first @ separates name from ref (refs do not contain @
	// for GitHub-hosted actions; docker:// was already filtered).
	action, ref, found := strings.Cut(uses, "@")
	if !found || action == "" || ref == "" {
		return "", "", false
	}
	// Require at least owner/name.
	if !strings.Contains(action, "/") {
		return "", "", false
	}
	return action, ref, true
}

func sortFindings(f []Finding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		if f[i].Line != f[j].Line {
			return f[i].Line < f[j].Line
		}
		if f[i].Action != f[j].Action {
			return f[i].Action < f[j].Action
		}
		return f[i].Ref < f[j].Ref
	})
}
