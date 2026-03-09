package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// GraphNode represents a node in the knowledge graph
type GraphNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Date     string `json:"date,omitempty"`
}

// GraphEdge represents an edge in the knowledge graph
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// GraphOutput is the JSON output structure
type GraphOutput struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Export knowledge graph of notes and sessions",
	Long: `Export the Note <-> Session cross-reference network as a graph.

Supports two output formats:
  --format dot   Graphviz DOT format for visualization (default)
  --format json  Adjacency list for programmatic use`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getUnifiedStore()
		if err != nil {
			return err
		}

		// Collect all notes and sessions
		notes, err := store.ListNotes("")
		if err != nil {
			return fmt.Errorf("failed to list notes: %w", err)
		}

		sessions, err := store.ListSessions(storage.SessionListOpts{})
		if err != nil {
			return fmt.Errorf("failed to list sessions: %w", err)
		}

		// Build node and edge lists
		var nodes []GraphNode
		var edges []GraphEdge

		// Build lookup: Session.SessionID (label) -> Session.ID (UUID)
		// Notes reference sessions by SessionID label, not by UUID
		sessionLabelToID := make(map[string]string)
		for _, s := range sessions {
			if s.SessionID != "" {
				sessionLabelToID[s.SessionID] = s.ID
			}
		}

		// Build lookup: truncated note ID -> full note ID
		// Sessions reference notes by truncated (8-char) IDs
		notePrefix := make(map[string]string)
		for _, n := range notes {
			notePrefix[truncateID(n.ID)] = n.ID
		}

		// Add note nodes and note->session edges
		for _, n := range notes {
			nodes = append(nodes, GraphNode{
				ID:       truncateID(n.ID),
				Type:     "note",
				Title:    n.Title,
				Category: n.Category,
			})

			if sid, ok := n.Metadata["session_id"]; ok && sid != "" {
				// Match session by SessionID label
				if fullSessionID, found := sessionLabelToID[sid]; found {
					edges = append(edges, GraphEdge{
						From: truncateID(n.ID),
						To:   truncateID(fullSessionID),
						Type: "from_session",
					})
				}
			}
		}

		// Add session nodes and session->note edges
		for _, s := range sessions {
			nodes = append(nodes, GraphNode{
				ID:    truncateID(s.ID),
				Type:  "session",
				Title: s.Title,
				Date:  s.Date,
			})

			for _, rn := range s.RelatedNotes {
				// RelatedNote.ID is already truncated (8-char prefix from markdown)
				// Validate it references an actual note
				if _, found := notePrefix[rn.ID]; found {
					edges = append(edges, GraphEdge{
						From: truncateID(s.ID),
						To:   rn.ID,
						Type: "related",
					})
				}
			}
		}

		// Output in requested format
		format := outputFormat
		if format == "text" {
			// Default to DOT for this command
			format = "dot"
		}

		switch format {
		case "json":
			output := GraphOutput{
				Nodes: nodes,
				Edges: edges,
			}
			// Ensure non-nil slices for clean JSON
			if output.Nodes == nil {
				output.Nodes = []GraphNode{}
			}
			if output.Edges == nil {
				output.Edges = []GraphEdge{}
			}
			return OutputJSON(output)

		case "dot":
			return outputDOT(nodes, edges)

		default:
			return fmt.Errorf("unsupported format %q for graph (use dot or json)", format)
		}
	},
}

// outputDOT writes the graph in Graphviz DOT format
func outputDOT(nodes []GraphNode, edges []GraphEdge) error {
	var b strings.Builder

	b.WriteString("digraph claudemem {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=filled];\n")

	// Notes (light blue)
	hasNotes := false
	for _, n := range nodes {
		if n.Type == "note" {
			if !hasNotes {
				b.WriteString("\n  // Notes (light blue)\n")
				hasNotes = true
			}
			label := escapeDOT(n.Title) + "\\n(" + escapeDOT(n.Category) + ")"
			fmt.Fprintf(&b, "  \"n_%s\" [label=\"%s\", fillcolor=\"#dbeafe\"];\n", n.ID, label)
		}
	}

	// Sessions (light green)
	hasSessions := false
	for _, n := range nodes {
		if n.Type == "session" {
			if !hasSessions {
				b.WriteString("\n  // Sessions (light green)\n")
				hasSessions = true
			}
			label := escapeDOT(n.Title) + "\\n(" + escapeDOT(n.Date) + ")"
			fmt.Fprintf(&b, "  \"s_%s\" [label=\"%s\", fillcolor=\"#d1fae5\"];\n", n.ID, label)
		}
	}

	// Edges
	if len(edges) > 0 {
		b.WriteString("\n  // Edges\n")
		for _, e := range edges {
			from := prefixedID(e.From, nodes)
			to := prefixedID(e.To, nodes)
			fmt.Fprintf(&b, "  \"%s\" -> \"%s\" [label=\"%s\"];\n", from, to, e.Type)
		}
	}

	b.WriteString("}\n")

	fmt.Print(b.String())
	return nil
}

// prefixedID returns the DOT node ID with type prefix (n_ or s_)
func prefixedID(id string, nodes []GraphNode) string {
	for _, n := range nodes {
		if n.ID == id {
			if n.Type == "note" {
				return "n_" + id
			}
			return "s_" + id
		}
	}
	return id
}

// escapeDOT escapes characters that are special in DOT label strings
func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// truncateID returns the first 8 characters of an ID for readability
func truncateID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func init() {
	rootCmd.AddCommand(graphCmd)
}
