package storage

import (
	"strings"
)

// ─── Historical Problems & Solutions corruption repair ──────────────────────
//
// RepairSessionMarkdown repairs the RECOVERABLE corruption left in stored
// session files by the historical Solution-loss defect (save path fused a
// problem's solution body into the problem line; fixed in ae4dff5). It is a
// pure function: same input → same output, no I/O, so the CLI can dry-run
// with zero writes and --apply stays byte-predictable.
//
// Repair semantics (all confined to the "## Problems & Solutions" section):
//
//	R1 fused split:        "- **Problem**: P **Solution**: S" splits at the
//	                       FIRST canonical delimiter; any later "**Solution**:"
//	                       text stays inside the solution body. The double
//	                       bold-colon shape ("**Problem:** P **Solution:** S"
//	                       inside a canonical problem field, observed in the
//	                       real corpus) splits the same way.
//	R2 orphan consumption: an empty standalone "  **Solution**:" directly
//	                       after a content-recovering fused split is consumed
//	                       (it was that pair's destroyed marker). It is NOT
//	                       consumed after a glued-empty strip — without
//	                       recovered content the marker is honest mode-A.
//	R3 glued-empty strip:  "P **Solution**:" (empty, end of line) glued onto a
//	                       problem line is stripped; P is kept verbatim and NO
//	                       solution content is fabricated. The same strip
//	                       applies to solution lines whose body is wrapped in
//	                       "**Problem**: " and ends with the glued empty
//	                       marker (the only real-corpus shape of that kind);
//	                       unwrapped prose bodies are left alone to avoid
//	                       false-positive text loss.
//	R4 bold-colon strip:   a leading "**Problem:**" / "**Solution:**" residue
//	                       token inside a field body is removed, text kept.
//	R5 untouchables:       standalone empty "  **Solution**:" pairs (mode-A,
//	                       solution destroyed at save time) and healthy pairs
//	                       are byte-preserved; other sections and non-canonical
//	                       section headers are never modified.
//	R6 idempotence:        repaired output contains no repairable signature,
//	                       so a second pass reports zero changes.

// RepairAction classifies one line-level repair.
type RepairAction string

const (
	// RepairActionFusedSplit splits a fused problem line into the canonical
	// two-line problem/solution pair (R1).
	RepairActionFusedSplit RepairAction = "fused-split"
	// RepairActionGluedEmptyStrip removes an empty "**Solution**:" marker
	// glued onto the end of a content line (R3).
	RepairActionGluedEmptyStrip RepairAction = "glued-empty-strip"
	// RepairActionBoldColonStrip removes a leading "**Problem:**" /
	// "**Solution:**" residue token from a field body (R4).
	RepairActionBoldColonStrip RepairAction = "bold-colon-strip"
	// RepairActionOrphanConsumed removes the stray empty marker line left
	// behind by a fused split (R2).
	RepairActionOrphanConsumed RepairAction = "orphan-consumed"
)

// RepairChange describes one repaired line. Line is the 1-based line number
// in the ORIGINAL content; Before is the original line; After holds the
// replacement lines (empty when the line was consumed).
type RepairChange struct {
	Line   int          `json:"line"`
	Action RepairAction `json:"action"`
	Before string       `json:"before"`
	After  []string     `json:"after"`
}

const (
	problemFieldPrefix  = "- **Problem**: "
	problemFieldMarker  = "**Problem**:"   // canonical problem marker (after bullet)
	solutionFieldPrefix = "**Solution**: " // canonical field opener (after indent)
	solutionFieldMarker = "**Solution**:"  // canonical marker, colon outside bold
	problemBoldColon    = "**Problem:**"   // bold-colon residue token
	solutionBoldColon   = "**Solution:**"  // bold-colon residue token
)

// RepairSessionMarkdown returns the repaired content and one RepairChange per
// repaired original line. Unchanged files get content back verbatim with an
// empty change list.
func RepairSessionMarkdown(content string) (string, []RepairChange) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	var changes []RepairChange
	inSection := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(line, "## ") {
			// Only the canonical section is in scope; variants like
			// "## Problems" are untouched (R5).
			inSection = strings.EqualFold(strings.TrimSpace(line), "## Problems & Solutions")
			out = append(out, line)
			continue
		}
		if !inSection {
			out = append(out, line)
			continue
		}

		if body, ok := strings.CutPrefix(line, problemFieldPrefix); ok {
			newLines, action, repaired := repairProblemBody(body)
			if !repaired {
				out = append(out, line)
				continue
			}
			changes = append(changes, RepairChange{Line: i + 1, Action: action, Before: line, After: newLines})
			out = append(out, newLines...)
			// R2: consume the stray empty marker only when the split actually
			// recovered solution content (fused-with-text).
			if action == RepairActionFusedSplit && i+1 < len(lines) && isEmptySolutionMarker(lines[i+1]) {
				changes = append(changes, RepairChange{Line: i + 2, Action: RepairActionOrphanConsumed, Before: lines[i+1], After: nil})
				i++ // consumed: nothing appended for the orphan line
			}
			continue
		}

		if newLine, action, repaired := repairSolutionLine(line); repaired {
			changes = append(changes, RepairChange{Line: i + 1, Action: action, Before: line, After: []string{newLine}})
			out = append(out, newLine)
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n"), changes
}

// repairProblemBody repairs one "- **Problem**: <body>" line. It returns the
// replacement lines (without the bullet prefix re-added? no — full lines),
// the action performed, and whether anything changed.
func repairProblemBody(body string) ([]string, RepairAction, bool) {
	// R1: fused canonical delimiter — split at the FIRST occurrence; later
	// marker text stays inside the solution body.
	if idx := strings.Index(body, solutionFieldMarker); idx >= 0 {
		problem := stripLeadingResidueToken(strings.TrimRight(body[:idx], " \t"), problemBoldColon)
		after := body[idx+len(solutionFieldMarker):]
		if strings.TrimSpace(after) != "" {
			solution := stripLeadingResidueToken(strings.TrimLeft(after, " \t"), solutionBoldColon)
			return []string{problemFieldPrefix + problem, "  " + solutionFieldPrefix + solution}, RepairActionFusedSplit, true
		}
		// R3: glued empty marker — strip it, fabricate nothing.
		return []string{problemFieldPrefix + problem}, RepairActionGluedEmptyStrip, true
	}

	// R4 / R1-variant: leading bold-colon problem token. When the remaining
	// body carries a bold-colon solution delimiter, split it as a fused
	// pair (the real corpus stores whole "**Problem:** P **Solution:** S"
	// pairs inside canonical problem fields).
	if rest, ok := cutResidueToken(body, problemBoldColon); ok {
		if idx := strings.Index(rest, solutionBoldColon); idx >= 0 {
			problem := strings.TrimRight(rest[:idx], " \t")
			after := rest[idx+len(solutionBoldColon):]
			if strings.TrimSpace(after) != "" {
				solution := strings.TrimLeft(after, " \t")
				return []string{problemFieldPrefix + problem, "  " + solutionFieldPrefix + solution}, RepairActionFusedSplit, true
			}
			return []string{problemFieldPrefix + problem}, RepairActionGluedEmptyStrip, true
		}
		return []string{problemFieldPrefix + rest}, RepairActionBoldColonStrip, true
	}

	return nil, "", false
}

// repairSolutionLine repairs an indented "  **Solution**: <body>" line with a
// non-empty body. Standalone empty markers (mode-A honest damage) are never
// touched (R5).
func repairSolutionLine(line string) (string, RepairAction, bool) {
	idx := strings.Index(line, solutionFieldPrefix)
	if idx < 0 || strings.TrimSpace(line[:idx]) != "" || idx == 0 {
		// Not an indented canonical solution field line.
		return "", "", false
	}
	prefix := line[:idx+len(solutionFieldPrefix)]
	body := line[idx+len(solutionFieldPrefix):]
	if strings.TrimSpace(body) == "" {
		return "", "", false // standalone empty marker — untouchable
	}

	// R3 on solution lines: strip a glued empty trailing marker, but only
	// within the observed wrapped signature (body starts with "**Problem**: "),
	// so prose bodies ending with the literal token are preserved.
	if strings.HasPrefix(body, problemFieldMarker+" ") && strings.HasSuffix(body, solutionFieldMarker) {
		before := body[:len(body)-len(solutionFieldMarker)]
		if strings.HasSuffix(before, " ") || strings.HasSuffix(before, "\t") {
			if stripped := strings.TrimRight(before, " \t"); stripped != "" {
				return prefix + stripped, RepairActionGluedEmptyStrip, true
			}
		}
	}

	// R4: leading bold-colon solution token inside the body.
	if rest, ok := cutResidueToken(body, solutionBoldColon); ok {
		if rest == "" {
			// Body was nothing but the residue token — degrades to the
			// standalone empty marker (mode-A shape), no fabrication.
			return strings.TrimRight(prefix, " \t"), RepairActionBoldColonStrip, true
		}
		return prefix + rest, RepairActionBoldColonStrip, true
	}

	return "", "", false
}

// isEmptySolutionMarker reports whether a line is a standalone empty
// "  **Solution**:" marker (mode-A shape).
func isEmptySolutionMarker(line string) bool {
	return strings.TrimSpace(line) == solutionFieldMarker
}

// cutResidueToken removes a leading bold-colon residue token followed by
// whitespace (or at end of string) and returns the trimmed remainder.
func cutResidueToken(body, token string) (string, bool) {
	if body == token {
		return "", true
	}
	if strings.HasPrefix(body, token+" ") || strings.HasPrefix(body, token+"\t") {
		return strings.TrimLeft(body[len(token):], " \t"), true
	}
	return "", false
}

// stripLeadingResidueToken is cutResidueToken with pass-through on no match.
func stripLeadingResidueToken(body, token string) string {
	if rest, ok := cutResidueToken(body, token); ok {
		return rest
	}
	return body
}
