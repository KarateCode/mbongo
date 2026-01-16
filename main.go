package main

import (
	"context"
	"fmt"
	"os"
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

const (
	defaultConnectionString = "mongodb://localhost:27017"
	leftPanelWidth          = 30
	docsPerPage             = 10
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			PaddingLeft(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Bold(true).
			PaddingLeft(1).
			PaddingRight(1)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			PaddingLeft(2)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	focusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)

	jsonKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81"))

	jsonStringStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("185"))

	jsonNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141"))

	jsonBoolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	jsonNullStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	jsonBracketStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("250"))

	caretStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	docCursorStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236"))

	paginationStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))
)

// Messages
type databasesLoadedMsg struct {
	databases []string
	err       error
}

type collectionsLoadedMsg struct {
	collections []string
	err         error
}

type documentsLoadedMsg struct {
	documents  []bson.M
	totalCount int64
	err        error
}

// Focus represents which panel is currently focused
type Focus int

const (
	FocusDatabases Focus = iota
	FocusCollections
	FocusDocuments
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

// Model represents the application state
type Model struct {
	client             *mongo.Client
	databases          []string
	collections        []string
	documents          []bson.M
	totalDocs          int64
	dbCursor           int
	collCursor         int
	focus              Focus
	width              int
	height             int
	err                error
	loading            bool
	loadingDocs        bool
	selectedCollection string
	// Document tree view
	docTree         []*JSONNode // Root nodes (one per document)
	flattenedTree   []*JSONNode // Flattened visible nodes for display
	docCursor       int         // Cursor position in flattened tree
	docScrollOffset int
}

func initialModel() Model {
	return Model{
		databases:   []string{},
		collections: []string{},
		documents:   []bson.M{},
		dbCursor:    0,
		collCursor:  0,
		focus:       FocusDatabases,
		loading:     true,
	}
}

func (m Model) Init() tea.Cmd {
	return connectToMongo
}

// getDocPanelHeight returns the visible height of the documents content area
func (m Model) getDocPanelHeight() int {
	availableHeight := m.height - 1
	if availableHeight < 10 {
		availableHeight = 10
	}
	leftPanelTotalHeight := availableHeight / 2
	rightPanelTotalHeight := leftPanelTotalHeight * 2
	// Inner height minus borders (2), minus header + blank line (2)
	return rightPanelTotalHeight - 4
}

func connectToMongo() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(defaultConnectionString))
	if err != nil {
		return databasesLoadedMsg{err: err}
	}

	// Ping to verify connection
	err = client.Ping(ctx, nil)
	if err != nil {
		return databasesLoadedMsg{err: err}
	}

	// List databases
	databases, err := client.ListDatabaseNames(ctx, map[string]interface{}{})
	if err != nil {
		return databasesLoadedMsg{err: err}
	}

	return databasesLoadedMsg{databases: databases}
}

func loadCollections(client *mongo.Client, dbName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		collections, err := client.Database(dbName).ListCollectionNames(ctx, map[string]interface{}{})
		if err != nil {
			return collectionsLoadedMsg{err: err}
		}

		sort.Strings(collections)

		return collectionsLoadedMsg{collections: collections}
	}
}

func loadDocuments(client *mongo.Client, dbName, collName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		coll := client.Database(dbName).Collection(collName)

		// Get total count
		totalCount, err := coll.CountDocuments(ctx, bson.M{})
		if err != nil {
			return documentsLoadedMsg{err: err}
		}

		// Fetch first N documents
		cursor, err := coll.Find(ctx, bson.M{}, options.Find().SetLimit(docsPerPage))
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "up", "k", "ctrl+p":
			switch m.focus {
			case FocusDatabases:
				if m.dbCursor > 0 {
					m.dbCursor--
					if len(m.databases) > 0 && m.client != nil {
						return m, loadCollections(m.client, m.databases[m.dbCursor])
					}
				}
			case FocusCollections:
				if m.collCursor > 0 {
					m.collCursor--
				}
			case FocusDocuments:
				if m.docCursor > 0 {
					m.docCursor--
					m.adjustScrollForCursor()
				}
			}

		case "down", "j", "ctrl+n":
			switch m.focus {
			case FocusDatabases:
				if m.dbCursor < len(m.databases)-1 {
					m.dbCursor++
					if m.client != nil {
						return m, loadCollections(m.client, m.databases[m.dbCursor])
					}
				}
			case FocusCollections:
				if m.collCursor < len(m.collections)-1 {
					m.collCursor++
				}
			case FocusDocuments:
				if m.docCursor < len(m.flattenedTree)-1 {
					m.docCursor++
					m.adjustScrollForCursor()
				}
			}

		case "right", "l":
			// Expand node
			if m.focus == FocusDocuments && len(m.flattenedTree) > 0 {
				node := m.flattenedTree[m.docCursor]
				if (node.IsObject || node.IsArray) && node.Collapsed {
					node.Collapsed = false
					m.rebuildFlattenedTree()
				}
			}

		case "left", "h":
			// Collapse node
			if m.focus == FocusDocuments && len(m.flattenedTree) > 0 {
				node := m.flattenedTree[m.docCursor]
				if (node.IsObject || node.IsArray) && !node.Collapsed {
					node.Collapsed = true
					m.rebuildFlattenedTree()
				}
			}

		case "tab":
			switch m.focus {
			case FocusDatabases:
				m.focus = FocusCollections
			case FocusCollections:
				if len(m.documents) > 0 {
					m.focus = FocusDocuments
				} else {
					m.focus = FocusDatabases
				}
			case FocusDocuments:
				m.focus = FocusDatabases
			}

		case "enter":
			switch m.focus {
			case FocusDatabases:
				m.focus = FocusCollections
			case FocusCollections:
				if len(m.collections) > 0 && m.client != nil && len(m.databases) > 0 {
					m.selectedCollection = m.collections[m.collCursor]
					m.loadingDocs = true
					m.docScrollOffset = 0
					m.docCursor = 0
					m.focus = FocusDocuments
					return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection)
				}
			case FocusDocuments:
				// Toggle expand/collapse on enter
				if len(m.flattenedTree) > 0 {
					node := m.flattenedTree[m.docCursor]
					if node.IsObject || node.IsArray {
						node.Collapsed = !node.Collapsed
						m.rebuildFlattenedTree()
					}
				}
			}

		case "ctrl+v":
			// Page down (half page) in documents panel
			if m.focus == FocusDocuments {
				halfPage := m.getDocPanelHeight() / 2
				if halfPage < 1 {
					halfPage = 1
				}
				m.docCursor += halfPage
				if m.docCursor >= len(m.flattenedTree) {
					m.docCursor = len(m.flattenedTree) - 1
				}
				if m.docCursor < 0 {
					m.docCursor = 0
				}
				m.adjustScrollForCursor()
			}

		case "alt+v":
			// Page up (half page) in documents panel
			if m.focus == FocusDocuments {
				halfPage := m.getDocPanelHeight() / 2
				if halfPage < 1 {
					halfPage = 1
				}
				m.docCursor -= halfPage
				if m.docCursor < 0 {
					m.docCursor = 0
				}
				m.adjustScrollForCursor()
			}

		case "ctrl+l":
			// Center cursor on screen
			if m.focus == FocusDocuments {
				visibleHeight := m.getDocPanelHeight()
				m.docScrollOffset = m.docCursor - visibleHeight/2
				if m.docScrollOffset < 0 {
					m.docScrollOffset = 0
				}
				maxOffset := len(m.flattenedTree) - visibleHeight
				if maxOffset < 0 {
					maxOffset = 0
				}
				if m.docScrollOffset > maxOffset {
					m.docScrollOffset = maxOffset
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case databasesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.databases = msg.databases

		// Store client for later use - reconnect to get it
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, _ := mongo.Connect(ctx, options.Client().ApplyURI(defaultConnectionString))
		m.client = client

		// Load collections for first database
		if len(m.databases) > 0 {
			return m, loadCollections(m.client, m.databases[0])
		}

	case collectionsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.collections = msg.collections
		m.collCursor = 0
		// Clear documents when switching databases
		m.documents = []bson.M{}
		m.selectedCollection = ""
		m.totalDocs = 0
		m.docTree = nil
		m.flattenedTree = nil

	case documentsLoadedMsg:
		m.loadingDocs = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.documents = msg.documents
		m.totalDocs = msg.totalCount
		// Build tree structure
		m.docTree = make([]*JSONNode, len(m.documents))
		for i, doc := range m.documents {
			m.docTree[i] = buildJSONTree(doc, 0)
			m.docTree[i].Collapsed = false // Expand root level
		}
		m.rebuildFlattenedTree()
		m.docCursor = 0
		m.docScrollOffset = 0
	}

	return m, nil
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

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}

	if m.loading {
		return "Connecting to MongoDB..."
	}

	// Calculate dimensions
	// Reserve 1 line for help text at bottom
	availableHeight := m.height - 1
	if availableHeight < 10 {
		availableHeight = 10
	}

	// Each left panel: innerHeight passed to Height() + 2 for borders = total rendered height
	// We want two left panels to fill availableHeight, so each gets half
	// If leftPanelTotalHeight is the rendered height, innerHeight = leftPanelTotalHeight - 2
	leftPanelTotalHeight := availableHeight / 2
	leftPanelInnerHeight := leftPanelTotalHeight - 2
	if leftPanelInnerHeight < 3 {
		leftPanelInnerHeight = 3
	}

	// Width calculation:
	// - leftPanelWidth is the inner width we pass to Width()
	// - Border adds 2 chars (left + right), padding adds 2 chars (1 each side)
	// - So total rendered left panel width = leftPanelWidth + 4
	// - Gap between panels = 1
	// - Right panel inner width = total - leftPanel rendered - gap - right panel border/padding (4)
	leftPanelRenderedWidth := leftPanelWidth + 4
	rightPanelWidth := m.width - leftPanelRenderedWidth - 1 - 4
	if rightPanelWidth < 20 {
		rightPanelWidth = 20
	}

	// Right panel total height = 2 * left panel total height
	rightPanelTotalHeight := leftPanelTotalHeight * 2
	rightPanelInnerHeight := rightPanelTotalHeight - 2
	if rightPanelInnerHeight < 3 {
		rightPanelInnerHeight = 3
	}

	// Build left panels
	dbContent := m.renderList(m.databases, m.dbCursor, m.focus == FocusDatabases, leftPanelInnerHeight-3)
	dbPanel := m.renderPanel("Databases", "", dbContent, m.focus == FocusDatabases, leftPanelWidth, leftPanelInnerHeight)

	collContent := m.renderList(m.collections, m.collCursor, m.focus == FocusCollections, leftPanelInnerHeight-3)
	collPanel := m.renderPanel("Collections", "", collContent, m.focus == FocusCollections, leftPanelWidth, leftPanelInnerHeight)

	leftPanel := lipgloss.JoinVertical(lipgloss.Left, dbPanel, collPanel)

	// Build right documents panel
	docPanel := m.renderDocumentsPanel(rightPanelWidth, rightPanelInnerHeight)

	// Join left and right panels
	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", docPanel)

	// Help text
	help := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render("↑/↓: navigate • ←/→: collapse/expand • enter: toggle • tab: switch panel • q: quit")

	return lipgloss.JoinVertical(lipgloss.Left, mainContent, help)
}

func (m Model) renderPanel(title, rightInfo, content string, focused bool, width, height int) string {
	style := panelStyle
	if focused {
		style = focusedPanelStyle
	}

	// Build header with title on left and info on right
	titleRendered := titleStyle.Render(title)
	var header string
	if rightInfo != "" {
		// Calculate spacing for right-aligned info
		titleWidth := lipgloss.Width(titleRendered)
		infoWidth := lipgloss.Width(rightInfo)
		spacing := width - titleWidth - infoWidth
		if spacing < 1 {
			spacing = 1
		}
		spaces := ""
		for i := 0; i < spacing; i++ {
			spaces += " "
		}
		header = titleRendered + spaces + paginationStyle.Render(rightInfo)
	} else {
		header = titleRendered
	}

	innerContent := lipgloss.JoinVertical(lipgloss.Left, header, "", content)

	// width and height are inner dimensions (passed directly to Width/Height)
	panel := style.
		Width(width).
		Height(height).
		Render(innerContent)

	return panel
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

		// Calculate pagination info
		endDoc := int64(docsPerPage)
		if endDoc > m.totalDocs {
			endDoc = m.totalDocs
		}
		rightInfo = fmt.Sprintf("1-%d of %d", endDoc, m.totalDocs)

		if len(m.documents) == 0 {
			content = normalStyle.Render("(no documents)")
		} else {
			// width is inner width, height is inner height
			// Content area: height - 2 (title line + blank line)
			content = m.renderDocumentTree(width, height-2)
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

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
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

// truncate truncates a string to maxLen, adding ellipsis if needed
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func (m Model) renderList(items []string, cursor int, focused bool, maxHeight int) string {
	if len(items) == 0 {
		return normalStyle.Render("(empty)")
	}

	// Calculate max item width: panel width - borders (2) - panel padding (2) - item padding (2)
	maxItemWidth := leftPanelWidth - 6

	// Calculate visible window around cursor
	visibleItems := maxHeight
	if visibleItems < 1 {
		visibleItems = 1
	}

	start := 0
	end := len(items)

	if len(items) > visibleItems {
		// Keep cursor in view with some context
		start = cursor - visibleItems/2
		if start < 0 {
			start = 0
		}
		end = start + visibleItems
		if end > len(items) {
			end = len(items)
			start = end - visibleItems
		}
	}

	var rendered string
	for i := start; i < end; i++ {
		item := truncate(items[i], maxItemWidth)
		if i == cursor && focused {
			rendered += selectedStyle.Render(item) + "\n"
		} else {
			rendered += normalStyle.Render(item) + "\n"
		}
	}

	return rendered
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}
