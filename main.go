package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
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
	FocusQuery
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
	currentPage        int // 0-indexed page number
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
	// Query input
	queryText    string        // The query text
	queryCursor  int           // Cursor position within query text
	querySpinner spinner.Model // Spinner for query loading
	queryLoading bool          // Whether a query is in progress
	queryFilter  bson.M        // Current active filter
	// Error modal
	errorModal   bool   // Whether to show error modal
	errorMessage string // Error message to display
}

func initialModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return Model{
		databases:    []string{},
		collections:  []string{},
		documents:    []bson.M{},
		dbCursor:     0,
		collCursor:   0,
		queryText:    "{}",
		queryCursor:  1, // Start between the braces
		querySpinner: s,
		queryFilter:  bson.M{},
		focus:        FocusDatabases,
		loading:      true,
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
	rightTotalHeight := leftPanelTotalHeight * 2

	// Query panel takes 3 lines (1 inner + 2 border)
	queryPanelTotalHeight := 3
	docPanelTotalHeight := rightTotalHeight - queryPanelTotalHeight

	// Inner height minus borders (2), minus header + blank line (2)
	return docPanelTotalHeight - 4
}

// saveDocument saves the modified document back to MongoDB
func (m Model) saveDocument(docIndex int, newDoc bson.M) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Get the original document's _id
		originalDoc := m.documents[docIndex]
		docID, ok := originalDoc["_id"]
		if !ok {
			return documentSavedMsg{err: fmt.Errorf("document has no _id field"), docIndex: docIndex}
		}

		// Ensure the new document has the original _id (MongoDB doesn't allow changing _id)
		// Remove any _id from the edited doc and use the original
		delete(newDoc, "_id")
		newDoc["_id"] = docID

		// Replace the document in MongoDB
		coll := m.client.Database(m.databases[m.dbCursor]).Collection(m.selectedCollection)
		_, err := coll.ReplaceOne(ctx, bson.M{"_id": docID}, newDoc)
		if err != nil {
			return documentSavedMsg{err: err, docIndex: docIndex}
		}

		return documentSavedMsg{err: nil, docIndex: docIndex, newDoc: newDoc}
	}
}

// expandTilde expands ~ to the user's home directory
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

// openInEditor opens the document at the given index in $EDITOR
func (m Model) openInEditor(docIndex int) tea.Cmd {
	doc := m.documents[docIndex]

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{err: err, docIndex: docIndex}
		}
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "mbongo-*.json")
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{err: err, docIndex: docIndex}
		}
	}
	tmpFileName := tmpFile.Name()

	// Write JSON to temp file
	if _, err := tmpFile.Write(jsonBytes); err != nil {
		tmpFile.Close()
		return func() tea.Msg {
			return editorFinishedMsg{err: err, docIndex: docIndex}
		}
	}
	tmpFile.Close()

	// Get editor from environment
	editorEnv := os.Getenv("EDITOR")
	if editorEnv == "" {
		editorEnv = "vi" // fallback
	}

	// Parse editor command - it may contain arguments (e.g., "emacs --init-directory=~/foo")
	parts := strings.Fields(editorEnv)
	editorCmd := parts[0]
	var editorArgs []string
	for _, arg := range parts[1:] {
		editorArgs = append(editorArgs, expandTilde(arg))
	}
	editorArgs = append(editorArgs, tmpFileName)

	// Create the editor command
	c := exec.Command(editorCmd, editorArgs...)

	// Store original JSON for comparison
	originalJSON := make([]byte, len(jsonBytes))
	copy(originalJSON, jsonBytes)

	// Use tea.ExecProcess to hand over the terminal to the editor
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{
			err:          err,
			tempFile:     tmpFileName,
			originalJSON: originalJSON,
			docIndex:     docIndex,
		}
	})
}

// relaxedJSONToStrict converts relaxed JavaScript-style JSON to strict JSON
// Handles: unquoted keys, single-quoted strings
func relaxedJSONToStrict(input string) string {
	var result strings.Builder
	i := 0
	n := len(input)

	for i < n {
		ch := input[i]

		switch {
		case ch == '"':
			// Already a double-quoted string, copy it as-is
			result.WriteByte(ch)
			i++
			for i < n {
				if input[i] == '\\' && i+1 < n {
					result.WriteByte(input[i])
					result.WriteByte(input[i+1])
					i += 2
				} else if input[i] == '"' {
					result.WriteByte(input[i])
					i++
					break
				} else {
					result.WriteByte(input[i])
					i++
				}
			}

		case ch == '\'':
			// Single-quoted string - convert to double quotes
			result.WriteByte('"')
			i++
			for i < n {
				if input[i] == '\\' && i+1 < n {
					// Handle escape sequences
					result.WriteByte(input[i])
					result.WriteByte(input[i+1])
					i += 2
				} else if input[i] == '"' {
					// Escape double quotes inside single-quoted strings
					result.WriteString("\\\"")
					i++
				} else if input[i] == '\'' {
					result.WriteByte('"')
					i++
					break
				} else {
					result.WriteByte(input[i])
					i++
				}
			}

		case ch == '{' || ch == ',' || ch == '[':
			// After these, we might have an unquoted key (for objects)
			result.WriteByte(ch)
			i++
			// Skip whitespace
			for i < n && (input[i] == ' ' || input[i] == '\t' || input[i] == '\n' || input[i] == '\r') {
				result.WriteByte(input[i])
				i++
			}
			// Check if next is an unquoted identifier (potential key)
			if i < n && ch != '[' && isIdentifierStart(input[i]) {
				// Read the identifier
				start := i
				for i < n && isIdentifierChar(input[i]) {
					i++
				}
				identifier := input[start:i]
				// Skip whitespace after identifier
				wsStart := i
				for i < n && (input[i] == ' ' || input[i] == '\t' || input[i] == '\n' || input[i] == '\r') {
					i++
				}
				// Check if followed by colon (making it a key)
				if i < n && input[i] == ':' {
					// It's a key - quote it
					result.WriteByte('"')
					result.WriteString(identifier)
					result.WriteByte('"')
					// Write the whitespace we skipped
					result.WriteString(input[wsStart:i])
				} else {
					// Not a key - could be a value like true, false, null, or a number
					result.WriteString(identifier)
					result.WriteString(input[wsStart:i])
				}
			}

		default:
			result.WriteByte(ch)
			i++
		}
	}

	return result.String()
}

// isIdentifierStart returns true if ch can start a JavaScript identifier
func isIdentifierStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

// isIdentifierChar returns true if ch can be part of a JavaScript identifier
func isIdentifierChar(ch byte) bool {
	return isIdentifierStart(ch) || (ch >= '0' && ch <= '9')
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

func loadDocuments(client *mongo.Client, dbName, collName string, page int, filter bson.M) tea.Cmd {
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

// editorFinishedMsg is sent when the editor closes
type editorFinishedMsg struct {
	err          error
	tempFile     string
	originalJSON []byte
	docIndex     int
}

// documentSavedMsg is sent when a document is saved to MongoDB
type documentSavedMsg struct {
	err      error
	docIndex int
	newDoc   bson.M
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle error modal dismissal FIRST - it takes priority over everything
		if m.errorModal {
			switch msg.String() {
			case "enter", "esc", "escape", " ":
				m.errorModal = false
				m.errorMessage = ""
			}
			return m, nil
		}

		// Handle query input when focused
		if m.focus == FocusQuery {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "tab":
				m.focus = FocusDocuments
			case "shift+tab":
				m.focus = FocusCollections
			case "left", "ctrl+b":
				if m.queryCursor > 0 {
					m.queryCursor--
				}
			case "right", "ctrl+f":
				if m.queryCursor < len(m.queryText) {
					m.queryCursor++
				}
			case "ctrl+a":
				m.queryCursor = 0
			case "ctrl+e":
				m.queryCursor = len(m.queryText)
			case "backspace", "ctrl+h":
				if m.queryCursor > 0 {
					m.queryText = m.queryText[:m.queryCursor-1] + m.queryText[m.queryCursor:]
					m.queryCursor--
				}
			case "delete", "ctrl+d":
				if m.queryCursor < len(m.queryText) {
					m.queryText = m.queryText[:m.queryCursor] + m.queryText[m.queryCursor+1:]
				}
			case "ctrl+k":
				// Kill to end of line
				m.queryText = m.queryText[:m.queryCursor]
			case "ctrl+u":
				// Kill to beginning of line
				m.queryText = m.queryText[m.queryCursor:]
				m.queryCursor = 0
			case "enter":
				// Execute query
				if m.selectedCollection != "" && m.client != nil {
					// Convert relaxed JS-style JSON to strict JSON, then parse
					strictJSON := relaxedJSONToStrict(m.queryText)
					var filter bson.M
					err := json.Unmarshal([]byte(strictJSON), &filter)
					if err != nil {
						m.errorModal = true
						m.errorMessage = fmt.Sprintf("Invalid query JSON: %v", err)
						return m, nil
					}
					m.queryFilter = filter
					m.queryLoading = true
					m.currentPage = 0
					m.docCursor = 0
					m.docScrollOffset = 0
					return m, tea.Batch(
						m.querySpinner.Tick,
						loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, 0, filter),
					)
				}
			default:
				// Insert regular characters
				if len(msg.String()) == 1 {
					ch := msg.String()[0]
					if ch >= 32 && ch < 127 {
						m.queryText = m.queryText[:m.queryCursor] + string(ch) + m.queryText[m.queryCursor:]
						m.queryCursor++
					}
				}
			}
			return m, nil
		}

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
				m.focus = FocusQuery
			case FocusQuery:
				m.focus = FocusDocuments
			case FocusDocuments:
				m.focus = FocusDatabases
			}

		case "shift+tab":
			switch m.focus {
			case FocusDatabases:
				m.focus = FocusDocuments
			case FocusCollections:
				m.focus = FocusDatabases
			case FocusQuery:
				m.focus = FocusCollections
			case FocusDocuments:
				m.focus = FocusQuery
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
					m.currentPage = 0
					m.queryFilter = bson.M{}
					m.queryText = "{}"
					m.queryCursor = 1
					m.focus = FocusDocuments
					return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, 0, m.queryFilter)
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

		case " ":
			// Spacebar also toggles expand/collapse in documents panel
			if m.focus == FocusDocuments && len(m.flattenedTree) > 0 {
				node := m.flattenedTree[m.docCursor]
				if node.IsObject || node.IsArray {
					node.Collapsed = !node.Collapsed
					m.rebuildFlattenedTree()
				}
			}

		case "n":
			// Next page of documents
			if m.focus == FocusDocuments && len(m.documents) > 0 {
				maxPage := (int(m.totalDocs) - 1) / docsPerPage
				if m.currentPage < maxPage {
					m.currentPage++
					m.loadingDocs = true
					m.docCursor = 0
					m.docScrollOffset = 0
					return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, m.currentPage, m.queryFilter)
				}
			}

		case "N":
			// Jump to last page of documents
			if m.focus == FocusDocuments && len(m.documents) > 0 {
				maxPage := (int(m.totalDocs) - 1) / docsPerPage
				if m.currentPage < maxPage {
					m.currentPage = maxPage
					m.loadingDocs = true
					m.docCursor = 0
					m.docScrollOffset = 0
					return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, m.currentPage, m.queryFilter)
				}
			}

		case "p":
			// Previous page of documents
			if m.focus == FocusDocuments && m.currentPage > 0 {
				m.currentPage--
				m.loadingDocs = true
				m.docCursor = 0
				m.docScrollOffset = 0
				return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, m.currentPage, m.queryFilter)
			}

		case "P":
			// Jump to first page of documents
			if m.focus == FocusDocuments && m.currentPage > 0 {
				m.currentPage = 0
				m.loadingDocs = true
				m.docCursor = 0
				m.docScrollOffset = 0
				return m, loadDocuments(m.client, m.databases[m.dbCursor], m.selectedCollection, 0, m.queryFilter)
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

		case "e":
			// Edit document in external editor
			if m.focus == FocusDocuments && len(m.documents) > 0 {
				docIndex := m.getDocumentIndexAtCursor()
				if docIndex >= 0 && docIndex < len(m.documents) {
					return m, m.openInEditor(docIndex)
				}
			}
		}

	case editorFinishedMsg:
		// Clean up temp file
		defer os.Remove(msg.tempFile)

		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		// Read the potentially modified file
		newJSON, err := os.ReadFile(msg.tempFile)
		if err != nil {
			m.err = fmt.Errorf("failed to read edited file: %w", err)
			return m, nil
		}

		// Check if content changed
		if string(newJSON) == string(msg.originalJSON) {
			// No changes, nothing to do
			return m, nil
		}

		// Parse the new JSON
		var newDoc bson.M
		if err := json.Unmarshal(newJSON, &newDoc); err != nil {
			m.err = fmt.Errorf("invalid JSON: %w", err)
			return m, nil
		}

		// Save to MongoDB
		return m, m.saveDocument(msg.docIndex, newDoc)

	case documentSavedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("failed to save document: %w", msg.err)
			return m, nil
		}

		// Update local state with the new document
		m.documents[msg.docIndex] = msg.newDoc
		m.docTree[msg.docIndex] = buildJSONTree(msg.newDoc, 0)
		m.docTree[msg.docIndex].Collapsed = false
		m.rebuildFlattenedTree()

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
		m.queryLoading = false
		if msg.err != nil {
			m.errorModal = true
			m.errorMessage = fmt.Sprintf("Query error: %v", msg.err)
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

	case spinner.TickMsg:
		if m.queryLoading {
			var cmd tea.Cmd
			m.querySpinner, cmd = m.querySpinner.Update(msg)
			return m, cmd
		}
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

	// Right side: Query panel (small) + Documents panel (rest)
	// Query panel: 1 line content + 2 border = 3 total height, inner height = 1
	queryPanelInnerHeight := 1
	queryPanelTotalHeight := queryPanelInnerHeight + 2

	// Right panels total height = 2 * left panel total height
	rightTotalHeight := leftPanelTotalHeight * 2

	// Documents panel gets remaining height (subtract 1 to align with left panels)
	docPanelTotalHeight := rightTotalHeight - queryPanelTotalHeight - 1
	docPanelInnerHeight := docPanelTotalHeight - 2
	if docPanelInnerHeight < 3 {
		docPanelInnerHeight = 3
	}

	// Build left panels
	dbContent := m.renderList(m.databases, m.dbCursor, m.focus == FocusDatabases, leftPanelInnerHeight-3)
	dbPanel := m.renderPanel("Databases", "", dbContent, m.focus == FocusDatabases, leftPanelWidth, leftPanelInnerHeight)

	collContent := m.renderList(m.collections, m.collCursor, m.focus == FocusCollections, leftPanelInnerHeight-3)
	collPanel := m.renderPanel("Collections", "", collContent, m.focus == FocusCollections, leftPanelWidth, leftPanelInnerHeight)

	leftPanel := lipgloss.JoinVertical(lipgloss.Left, dbPanel, collPanel)

	// Build right panels
	queryPanel := m.renderQueryPanel(rightPanelWidth, queryPanelInnerHeight)
	docPanel := m.renderDocumentsPanel(rightPanelWidth, docPanelInnerHeight)
	rightPanel := lipgloss.JoinVertical(lipgloss.Left, queryPanel, docPanel)

	// Join left and right panels
	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)

	// Help text
	help := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render("↑/↓: navigate • ←/→/space: collapse/expand • n/p: next/prev page • e: edit • tab: switch • q: quit")

	result := lipgloss.JoinVertical(lipgloss.Left, mainContent, help)

	// Overlay error modal if active
	if m.errorModal {
		result = m.renderErrorModal(result)
	}

	return result
}

func (m Model) renderErrorModal(background string) string {
	// Create modal box
	modalWidth := 50
	if m.width-10 < modalWidth {
		modalWidth = m.width - 10
	}
	if modalWidth < 20 {
		modalWidth = 20
	}

	// Wrap error message
	msg := m.errorMessage
	maxMsgWidth := modalWidth - 6 // Account for padding and border
	if len(msg) > maxMsgWidth {
		// Simple word wrap
		var lines []string
		words := strings.Fields(msg)
		line := ""
		for _, word := range words {
			if len(line)+len(word)+1 > maxMsgWidth {
				if line != "" {
					lines = append(lines, line)
				}
				line = word
			} else {
				if line != "" {
					line += " "
				}
				line += word
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
		msg = strings.Join(lines, "\n")
	}

	errorTitleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("196"))

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)

	content := lipgloss.JoinVertical(lipgloss.Left,
		errorTitleStyle.Render("Error"),
		"",
		msg,
		"",
		hintStyle.Render("Press Enter, Esc, or Space to dismiss"),
	)

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("196")).
		BorderBackground(lipgloss.Color("235")).
		Background(lipgloss.Color("235")).
		Padding(1, 2).
		Width(modalWidth)

	modal := modalStyle.Render(content)

	// Use lipgloss.Place to center the modal on screen
	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		modal,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("236")),
	)
}

func (m Model) renderPanel(title, rightInfo, content string, focused bool, width, height int) string {
	style := panelStyle
	if focused {
		style = focusedPanelStyle
	}

	// Actual content width after padding (0, 1) = 2 chars for horizontal padding
	contentWidth := width - 2

	// Build header with title on left and info on right
	var header string
	if rightInfo != "" {
		rightRendered := paginationStyle.Render(rightInfo)
		infoWidth := lipgloss.Width(rightRendered)

		// Calculate max width for title: content width - info width - minimum 1 space
		maxTitleWidth := contentWidth - infoWidth - 1
		if maxTitleWidth < 10 {
			maxTitleWidth = 10
		}

		// Truncate title if needed (before styling)
		displayTitle := title
		if len(displayTitle) > maxTitleWidth-1 { // -1 for the padding in titleStyle
			displayTitle = displayTitle[:maxTitleWidth-4] + "..."
		}
		titleRendered := titleStyle.Render(displayTitle)
		titleWidth := lipgloss.Width(titleRendered)

		spacing := contentWidth - titleWidth - infoWidth
		if spacing < 1 {
			spacing = 1
		}
		spaces := strings.Repeat(" ", spacing)
		header = titleRendered + spaces + rightRendered
	} else {
		// No right info, just truncate title if needed
		displayTitle := title
		if len(displayTitle) > contentWidth-1 {
			displayTitle = displayTitle[:contentWidth-4] + "..."
		}
		header = titleStyle.Render(displayTitle)
	}

	// Truncate content to fit available height
	// Available content height = total inner height - 2 (header + blank line)
	contentHeight := height - 2
	if contentHeight < 0 {
		contentHeight = 0
	}
	contentLines := strings.Split(content, "\n")
	if len(contentLines) > contentHeight {
		contentLines = contentLines[:contentHeight]
	}
	content = strings.Join(contentLines, "\n")

	// Build the panel content with fixed structure:
	// Line 1: header
	// Line 2: blank
	// Lines 3+: content
	innerContent := lipgloss.JoinVertical(lipgloss.Left, header, "", content)

	// width and height are inner dimensions (passed directly to Width/Height)
	panel := style.
		Width(width).
		Height(height).
		Render(innerContent)

	return panel
}

func (m Model) renderQueryPanel(width, height int) string {
	// Render query text with cursor
	contentWidth := width - 2 // Account for padding

	// Reserve space for spinner if loading
	spinnerWidth := 0
	spinnerStr := ""
	if m.queryLoading {
		spinnerStr = m.querySpinner.View()
		spinnerWidth = lipgloss.Width(spinnerStr) + 1 // +1 for space
	}

	availableWidth := contentWidth - spinnerWidth

	var content string
	if m.focus == FocusQuery {
		// Show cursor
		before := m.queryText[:m.queryCursor]
		after := m.queryText[m.queryCursor:]
		cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
		if m.queryCursor < len(m.queryText) {
			cursor = lipgloss.NewStyle().Reverse(true).Render(string(m.queryText[m.queryCursor]))
			after = m.queryText[m.queryCursor+1:]
		}
		content = before + cursor + after
	} else {
		content = m.queryText
	}

	// Truncate if too long
	if len(m.queryText) > availableWidth {
		content = m.queryText[:availableWidth-3] + "..."
	}

	// Add spinner on the right if loading
	if m.queryLoading {
		// Pad content to push spinner to the right
		contentLen := lipgloss.Width(content)
		padding := availableWidth - contentLen
		if padding > 0 {
			content = content + strings.Repeat(" ", padding)
		}
		content = content + " " + spinnerStr
	}

	return m.renderQueryPanelFrame("Query", content, m.focus == FocusQuery, width, height)
}

func (m Model) renderQueryPanelFrame(title, content string, focused bool, width, height int) string {
	style := panelStyle
	if focused {
		style = focusedPanelStyle
	}

	contentWidth := width - 2
	titleRendered := titleStyle.Render(title)

	// Truncate title if needed
	if lipgloss.Width(titleRendered) > contentWidth {
		title = title[:contentWidth-4] + "..."
		titleRendered = titleStyle.Render(title)
	}

	// For the query panel, we just show title on one line, content directly below (no blank line)
	// Since height is 1, we only have room for the content
	innerContent := lipgloss.JoinVertical(lipgloss.Left, titleRendered, content)

	panel := style.
		Width(width).
		Height(height + 1). // +1 for the title line
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
