package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format names accepted by the CLI (aligned with other packages).
const (
	FormatTable = "table"
	FormatJSON  = "json"
)

// Write formats a transport result. format is "table" (default) or "json".
func Write(w io.Writer, result *Result, format string) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", FormatTable:
		return writeTable(w, result)
	case FormatJSON:
		return writeJSON(w, result)
	default:
		return fmt.Errorf("unknown format %q (want %q or %q)", format, FormatTable, FormatJSON)
	}
}

func writeTable(w io.Writer, r *Result) error {
	// Single summary line for humans; details are in Message / PR URL.
	var line string
	switch {
	case r.Skipped:
		line = "transport: skipped (no pin changes)"
	case r.DryRun && r.Mode == ModePR:
		line = fmt.Sprintf("transport: dry-run would open PR (branch %s → %s)", r.Branch, r.Base)
	case r.DryRun && r.Mode == ModeCommit:
		line = "transport: dry-run would create local commit"
	case r.Mode == ModePR && r.PRURL != "":
		line = fmt.Sprintf("transport: pr %s (branch %s, commit %s)", r.PRURL, r.Branch, shortSHA(r.CommitSHA))
	case r.Mode == ModeCommit && r.CommitSHA != "":
		line = fmt.Sprintf("transport: commit %s", shortSHA(r.CommitSHA))
	default:
		if r.Message != "" {
			line = "transport: " + r.Message
		} else {
			line = fmt.Sprintf("transport: mode=%s", r.Mode)
		}
	}
	if _, err := fmt.Fprintln(w, line); err != nil {
		return fmt.Errorf("write transport: %w", err)
	}
	return nil
}

func writeJSON(w io.Writer, result *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
