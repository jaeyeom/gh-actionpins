package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format names accepted by the CLI.
const (
	FormatTable = "table"
	FormatJSON  = "json"
)

// Write formats result to w. format is "table" (default) or "json".
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

func writeTable(w io.Writer, result *Result) error {
	if len(result.Entries) == 0 {
		if _, err := fmt.Fprintln(w, "No action references found."); err != nil {
			return fmt.Errorf("write table: %w", err)
		}
		return writeSummaryLine(w, result.Summary)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "STATUS\tFILE\tLINE\tACTION\tREF\tCATALOG_SHA\tDETAIL"); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}
	for _, e := range result.Entries {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			e.Status, e.File, e.Line, e.Action, e.Ref, e.CatalogSHA, e.Detail); err != nil {
			return fmt.Errorf("write table row: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	return writeSummaryLine(w, result.Summary)
}

func writeSummaryLine(w io.Writer, s Summary) error {
	drift := "clean"
	if s.Drift {
		drift = "drift"
	}
	_, err := fmt.Fprintf(w, "summary: %s  ok=%d mismatch=%d unpinned=%d unknown=%d\n",
		drift, s.OK, s.Mismatch, s.Unpinned, s.Unknown)
	if err != nil {
		return fmt.Errorf("write summary: %w", err)
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
