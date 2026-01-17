package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// JSONNode represents a node in the JSON tree
type JSONNode struct {
	Key       string      // Key name (empty for array elements or root)
	Value     interface{} // The actual value (for leaf nodes)
	Children  []*JSONNode // Child nodes (for objects/arrays)
	IsObject  bool        // True if this is an object
	IsArray   bool        // True if this is an array
	Collapsed bool        // True if collapsed
	Depth     int         // Indentation depth
}

func loadDocuments(client *mongo.Client, dbName, collName string, page int, filter bson.M) func() tea.Msg {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		coll := client.Database(dbName).Collection(collName)

		// Use provided filter or empty filter
		if filter == nil {
			filter = bson.M{}
		}

		// Get total count matching filter
		totalCount, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			return documentsLoadedMsg{err: err}
		}

		// Fetch documents for the current page
		skip := int64(page * docsPerPage)
		cursor, err := coll.Find(ctx, filter, options.Find().SetSkip(skip).SetLimit(docsPerPage))
		if err != nil {
			return documentsLoadedMsg{err: err}
		}
		defer cursor.Close(ctx)

		var documents []bson.M
		if err := cursor.All(ctx, &documents); err != nil {
			return documentsLoadedMsg{err: err}
		}

		return documentsLoadedMsg{documents: documents, totalCount: totalCount}
	}
}

// buildJSONTree converts a BSON document to a tree structure
func buildJSONTree(doc bson.M, depth int) *JSONNode {
	node := &JSONNode{
		IsObject:  true,
		Collapsed: depth > 0, // Collapse all except root
		Depth:     depth,
		Children:  make([]*JSONNode, 0),
	}

	// Get sorted keys for consistent ordering
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := doc[key]
		child := buildValueNode(key, value, depth+1)
		node.Children = append(node.Children, child)
	}

	return node
}

// buildValueNode creates a node for any value type
func buildValueNode(key string, value interface{}, depth int) *JSONNode {
	node := &JSONNode{
		Key:       key,
		Depth:     depth,
		Collapsed: true,
	}

	switch v := value.(type) {
	case bson.M:
		node.IsObject = true
		node.Children = make([]*JSONNode, 0)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := buildValueNode(k, v[k], depth+1)
			node.Children = append(node.Children, child)
		}
	case bson.A:
		node.IsArray = true
		node.Children = make([]*JSONNode, 0)
		for i, item := range v {
			child := buildValueNode(fmt.Sprintf("[%d]", i), item, depth+1)
			node.Children = append(node.Children, child)
		}
	default:
		node.Value = value
	}

	return node
}

// flattenTree creates a flat list of visible nodes for rendering
func flattenTree(nodes []*JSONNode) []*JSONNode {
	var result []*JSONNode
	for i, node := range nodes {
		result = append(result, flattenNode(node, i > 0)...)
	}
	return result
}

func flattenNode(node *JSONNode, addSeparator bool) []*JSONNode {
	var result []*JSONNode

	if addSeparator && node.Depth == 0 {
		// Add a separator node between documents
		result = append(result, &JSONNode{Key: "---", Depth: -1})
	}

	result = append(result, node)

	if !node.Collapsed && (node.IsObject || node.IsArray) {
		for _, child := range node.Children {
			result = append(result, flattenNode(child, false)...)
		}
	}

	return result
}

// rebuildFlattenedTree rebuilds the flattened view from the tree
func (m *Model) rebuildFlattenedTree() {
	m.flattenedTree = flattenTree(m.docTree)
}

// getDocumentIndexAtCursor returns the index of the document the cursor is on
func (m Model) getDocumentIndexAtCursor() int {
	if len(m.flattenedTree) == 0 || m.docCursor >= len(m.flattenedTree) {
		return -1
	}

	// Walk backwards from cursor to find the root node (depth 0)
	// Count how many root nodes we pass
	docIndex := 0
	for i := m.docCursor; i >= 0; i-- {
		node := m.flattenedTree[i]
		if node.Depth == 0 && node.IsObject {
			// This is a document root
			// Count how many doc roots are before this position
			docIndex = 0
			for j := 0; j <= i; j++ {
				n := m.flattenedTree[j]
				if n.Depth == 0 && n.IsObject {
					docIndex++
				}
			}
			return docIndex - 1
		}
	}
	return 0
}

// getDocPanelHeight returns the visible height of the documents content area
func (m Model) getDocPanelHeight() int {
	availableHeight := m.height - 1
	if availableHeight < 10 {
		availableHeight = 10
	}
	leftPanelTotalHeight := availableHeight / 2
	rightTotalHeight := leftPanelTotalHeight * 2

	// Query panel takes 3 lines (1 inner + 2 border)
	queryPanelTotalHeight := 3
	docPanelTotalHeight := rightTotalHeight - queryPanelTotalHeight

	// Inner height minus borders (2), minus header + blank line (2)
	return docPanelTotalHeight - 4
}

// adjustScrollForCursor ensures the cursor is visible
func (m *Model) adjustScrollForCursor() {
	visibleHeight := m.getDocPanelHeight()
	if m.docCursor < m.docScrollOffset {
		m.docScrollOffset = m.docCursor
	} else if m.docCursor >= m.docScrollOffset+visibleHeight {
		m.docScrollOffset = m.docCursor - visibleHeight + 1
	}
}

func (m Model) renderDocumentsPanel(width, height int) string {
	var title string
	var rightInfo string
	var content string

	if m.selectedCollection == "" {
		title = "Documents"
		content = normalStyle.Render("Select a collection to view documents")
	} else if m.loadingDocs {
		title = fmt.Sprintf("Documents in %s", m.selectedCollection)
		content = normalStyle.Render("Loading...")
	} else {
		title = fmt.Sprintf("Documents in %s", m.selectedCollection)

		// Calculate pagination info based on current page
		startDoc := int64(m.currentPage*docsPerPage) + 1
		endDoc := int64((m.currentPage + 1) * docsPerPage)
		if endDoc > m.totalDocs {
			endDoc = m.totalDocs
		}
		if m.totalDocs == 0 {
			startDoc = 0
		}
		rightInfo = fmt.Sprintf("%d-%d of %d", startDoc, endDoc, m.totalDocs)

		if len(m.documents) == 0 {
			content = normalStyle.Render("(no documents)")
		} else {
			// width is inner width passed to Width(), but padding(0,1) takes 2 more chars
			// So actual content width = width - 2
			// Content area height: height - 2 (title line + blank line)
			content = m.renderDocumentTree(width-2, height-2)
		}
	}

	return m.renderPanel(title, rightInfo, content, m.focus == FocusDocuments, width, height)
}

func (m Model) renderDocumentTree(maxWidth, maxHeight int) string {
	if len(m.flattenedTree) == 0 {
		return ""
	}

	var lines []string

	// Apply scroll offset
	start := m.docScrollOffset
	if start > len(m.flattenedTree) {
		start = len(m.flattenedTree)
	}
	end := start + maxHeight
	if end > len(m.flattenedTree) {
		end = len(m.flattenedTree)
	}

	for i := start; i < end; i++ {
		node := m.flattenedTree[i]
		line := m.renderNode(node, maxWidth)

		// Highlight cursor line
		if i == m.docCursor && m.focus == FocusDocuments {
			line = docCursorStyle.Render(line)
		}

		lines = append(lines, line)
	}

	// Join with newlines directly instead of lipgloss.JoinVertical
	return strings.Join(lines, "\n")
}

func (m Model) renderNode(node *JSONNode, maxWidth int) string {
	if node.Depth == -1 {
		// Separator
		return strings.Repeat("─", maxWidth)
	}

	indent := strings.Repeat("  ", node.Depth)
	var line string

	if node.IsObject || node.IsArray {
		// Collapsible node
		caret := "▶"
		if !node.Collapsed {
			caret = "▼"
		}
		caret = caretStyle.Render(caret)

		var bracket string
		var closeBracket string
		var childCount int
		if node.IsObject {
			bracket = "{"
			closeBracket = "}"
			childCount = len(node.Children)
		} else {
			bracket = "["
			closeBracket = "]"
			childCount = len(node.Children)
		}

		if node.Key != "" {
			keyStr := jsonKeyStyle.Render(fmt.Sprintf("%q", node.Key))
			if node.Collapsed {
				summary := fmt.Sprintf(" %d items", childCount)
				line = fmt.Sprintf("%s%s %s: %s...%s%s", indent, caret, keyStr,
					jsonBracketStyle.Render(bracket),
					paginationStyle.Render(summary),
					jsonBracketStyle.Render(closeBracket))
			} else {
				line = fmt.Sprintf("%s%s %s: %s", indent, caret, keyStr, jsonBracketStyle.Render(bracket))
			}
		} else {
			// Root object
			if node.Collapsed {
				summary := fmt.Sprintf(" %d items", childCount)
				line = fmt.Sprintf("%s%s %s...%s%s", indent, caret,
					jsonBracketStyle.Render(bracket),
					paginationStyle.Render(summary),
					jsonBracketStyle.Render(closeBracket))
			} else {
				line = fmt.Sprintf("%s%s %s", indent, caret, jsonBracketStyle.Render(bracket))
			}
		}
	} else {
		// Leaf node
		valueStr := formatValue(node.Value)
		if node.Key != "" && !strings.HasPrefix(node.Key, "[") {
			keyStr := jsonKeyStyle.Render(fmt.Sprintf("%q", node.Key))
			line = fmt.Sprintf("%s  %s: %s", indent, keyStr, valueStr)
		} else if node.Key != "" {
			// Array index
			keyStr := paginationStyle.Render(node.Key)
			line = fmt.Sprintf("%s  %s: %s", indent, keyStr, valueStr)
		} else {
			line = fmt.Sprintf("%s  %s", indent, valueStr)
		}
	}

	// Truncate if too long
	// Note: This is approximate since we have ANSI codes
	if lipgloss.Width(line) > maxWidth {
		// Simple truncation - not perfect with ANSI codes but good enough
		runes := []rune(line)
		if len(runes) > maxWidth-3 {
			line = string(runes[:maxWidth-3]) + "..."
		}
	}

	return line
}

func formatValue(value interface{}) string {
	if value == nil {
		return jsonNullStyle.Render("null")
	}

	switch v := value.(type) {
	case string:
		return jsonStringStyle.Render(fmt.Sprintf("%q", v))
	case int, int32, int64, float32, float64:
		return jsonNumberStyle.Render(fmt.Sprintf("%v", v))
	case bool:
		return jsonBoolStyle.Render(fmt.Sprintf("%v", v))
	case primitive.ObjectID:
		return jsonStringStyle.Render(fmt.Sprintf("ObjectId(%q)", v.Hex()))
	case primitive.DateTime:
		t := v.Time()
		return jsonStringStyle.Render(fmt.Sprintf("ISODate(%q)", t.Format(time.RFC3339)))
	case primitive.Timestamp:
		return jsonNumberStyle.Render(fmt.Sprintf("Timestamp(%d, %d)", v.T, v.I))
	case primitive.Decimal128:
		return jsonNumberStyle.Render(v.String())
	case primitive.Binary:
		return jsonStringStyle.Render(fmt.Sprintf("Binary(%q)", v.Subtype))
	case primitive.Regex:
		return jsonStringStyle.Render(fmt.Sprintf("/%s/%s", v.Pattern, v.Options))
	default:
		// Try to convert to string
		s := fmt.Sprintf("%v", v)
		// Check if it looks like a number
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return jsonNumberStyle.Render(s)
		}
		return jsonStringStyle.Render(fmt.Sprintf("%q", s))
	}
}
