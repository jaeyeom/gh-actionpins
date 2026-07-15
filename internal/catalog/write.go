package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// UpdateActionPin rewrites version, sha, and approved_at for one action in the
// catalog file at path. Other keys, key order, and comments are preserved as
// much as practical via the yaml.v3 node tree.
//
// The action must already exist under actions:. The resulting file is
// re-validated before the write is finalized (temp file + rename).
func UpdateActionPin(path, action string, pin Action) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return fmt.Errorf("action name is required")
	}
	if err := validateAction(action, pin); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read catalog %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("catalog %s: parse yaml: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("catalog %s: empty yaml document", path)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("catalog %s: root must be a mapping", path)
	}

	actionsNode := mapValue(root, "actions")
	if actionsNode == nil {
		return fmt.Errorf("catalog %s: actions: required map is missing", path)
	}
	if actionsNode.Kind != yaml.MappingNode {
		return fmt.Errorf("catalog %s: actions: must be a mapping", path)
	}

	entry := mapValue(actionsNode, action)
	if entry == nil {
		return fmt.Errorf("catalog %s: action %q not in catalog", path, action)
	}
	if entry.Kind != yaml.MappingNode {
		return fmt.Errorf("catalog %s: actions[%s]: must be a mapping", path, action)
	}

	setMapString(entry, "version", strings.TrimSpace(pin.Version))
	setMapString(entry, "sha", strings.TrimSpace(pin.SHA))
	setMapString(entry, "approved_at", strings.TrimSpace(pin.ApprovedAt))

	// Validate the full document after the edit (policy, other actions, etc.).
	out, err := encodeDocument(&doc)
	if err != nil {
		return fmt.Errorf("catalog %s: encode: %w", path, err)
	}
	if _, err := Parse(out); err != nil {
		return fmt.Errorf("catalog %s: after update: %w", path, err)
	}

	if err := writeFileAtomic(path, out); err != nil {
		return fmt.Errorf("write catalog %s: %w", path, err)
	}
	return nil
}

func encodeDocument(doc *yaml.Node) ([]byte, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("yaml encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("yaml encoder close: %w", err)
	}
	return []byte(buf.String()), nil
}

func writeFileAtomic(path string, data []byte) error {
	// Prefer same-directory temp for atomic rename on the same filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on failure paths.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	// Preserve existing file mode when possible.
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	tmpName = "" // cancel deferred remove
	return nil
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k != nil && k.Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// setMapString sets key to a plain string scalar, preserving style of an
// existing value when present. Appends the key at the end when missing.
//
// Tag is left empty so values that look like dates (approved_at) stay unquoted
// like the rest of the example catalogs.
func setMapString(n *yaml.Node, key, val string) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k == nil || k.Value != key {
			continue
		}
		v := n.Content[i+1]
		if v == nil {
			v = &yaml.Node{}
			n.Content[i+1] = v
		}
		style := v.Style
		*v = yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: val,
			Style: style,
		}
		return
	}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val},
	)
}
