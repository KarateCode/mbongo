package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultConnectionString = "mongodb://localhost:27017"
	leftPanelWidth          = 30
	docsPerPage             = 10
)

// Screen represents which screen is currently displayed
type Screen int

const (
	ScreenConnections Screen = iota
	ScreenMain
)

// Focus represents which panel is currently focused
type Focus int

const (
	FocusDatabases Focus = iota
	FocusCollections
	FocusQuery
	FocusDocuments
)

// Model represents the application state
type Model struct {
	// Screen state
	screen           Screen
	connections      []Connection // Available connections
	connCursor       int          // Cursor for connections list
	connectionString string       // Originally selected connection string
	activeConnString string       // Actual connection string in use (may be tunneled)
	sshAlias         string       // SSH alias for tunneling (empty for direct)
	sshTunnel        *SSHTunnel   // Active SSH tunnel (nil for direct)
	// MongoDB state
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
	selectedDatabase   string
	selectedCollection string
	explicitDBSelect   bool // True if user pressed Enter to select DB (show errors)
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
	// Collection search
	collSearchActive    bool            // Whether search mode is active
	collSearchInput     textinput.Model // Search input field
	collFiltered        []string        // Filtered collection names
	collFilteredIndices []int           // Indices into original collections slice
	// Database search
	dbSearchActive    bool            // Whether search mode is active
	dbSearchInput     textinput.Model // Search input field
	dbFiltered        []string        // Filtered database names
	dbFilteredIndices []int           // Indices into original databases slice
	// New/Edit connection modal
	newConnModal         bool            // Whether the new connection modal is open
	newConnNameInput     textinput.Model // Name input field
	newConnSSHAliasInput textinput.Model // SSH alias input field
	newConnStringInput   textinput.Model // Connection string input field
	newConnFocusField    int             // 0=name, 1=ssh alias, 2=connection string
	editingConnIndex     int             // Index of connection being edited, -1 if creating new
	editingConnOldName   string          // Original name of connection being edited (for DB update)
	// Delete confirmation modal
	deleteConnModal bool // Whether the delete confirmation modal is open
	deleteConnIndex int  // Index of connection to delete
	// Connection search
	connSearchActive    bool            // Whether search mode is active
	connSearchInput     textinput.Model // Search input field
	connFiltered        []Connection    // Filtered connections
	connFilteredIndices []int           // Indices into original connections slice
	// Document search
	docSearchActive  bool            // Whether document search is active
	docSearchInput   textinput.Model // Search input field
	docSearchMatches []int           // Indices of matching lines in flattenedTree
	docSearchCurrent int             // Current match index (-1 if none)
	// Auto-select database from env var
	autoSelectDB string // Database name to auto-select (from $DATABASE_NAME)
}

func initialModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	// New connection modal inputs
	nameInput := textinput.New()
	nameInput.Placeholder = "My Connection"
	nameInput.CharLimit = 50
	nameInput.Width = 40

	sshAliasInput := textinput.New()
	sshAliasInput.Placeholder = "(blank for direct connection)"
	sshAliasInput.CharLimit = 100
	sshAliasInput.Width = 40

	connStringInput := textinput.New()
	connStringInput.Placeholder = "mongodb://localhost:27017"
	connStringInput.CharLimit = 200
	connStringInput.Width = 40

	// Document search input
	docSearchInput := textinput.New()
	docSearchInput.Placeholder = ""
	docSearchInput.CharLimit = 100
	docSearchInput.Width = 30
	docSearchInput.Prompt = "Search: "

	// Connection search input
	connSearchInput := textinput.New()
	connSearchInput.Placeholder = "search..."
	connSearchInput.CharLimit = 100
	connSearchInput.Width = 30

	// Check for DATABASE_NAME env var for auto-selection
	autoSelectDB := os.Getenv("DATABASE_NAME")

	return Model{
		screen:               ScreenConnections,
		connections:          defaultConnections,
		connCursor:           0,
		databases:            []string{},
		collections:          []string{},
		documents:            []bson.M{},
		dbCursor:             0,
		collCursor:           0,
		queryText:            "{}",
		queryCursor:          1, // Start between the braces
		querySpinner:         s,
		queryFilter:          bson.M{},
		focus:                FocusDatabases,
		loading:              false, // Don't start loading until connection is selected
		collSearchInput:      newCollectionSearchInput(),
		collFiltered:         []string{},
		dbSearchInput:        newDatabaseSearchInput(),
		dbFiltered:           []string{},
		newConnNameInput:     nameInput,
		newConnSSHAliasInput: sshAliasInput,
		newConnStringInput:   connStringInput,
		newConnFocusField:    0,
		connSearchInput:      connSearchInput,
		connFiltered:         []Connection{},
		connFilteredIndices:  []int{},
		docSearchInput:       docSearchInput,
		docSearchMatches:     []int{},
		docSearchCurrent:     -1,
		autoSelectDB:         autoSelectDB,
	}
}

// connectionsLoadedMsg is sent when connections are loaded from the database
type connectionsLoadedMsg struct {
	connections []Connection
	err         error
}

func (m Model) Init() tea.Cmd {
	// Initialize DB and load connections
	return func() tea.Msg {
		if err := initDB(); err != nil {
			return connectionsLoadedMsg{err: err}
		}
		connections, err := loadConnections()
		if err != nil {
			return connectionsLoadedMsg{err: err}
		}
		return connectionsLoadedMsg{connections: connections}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle connections screen
		if m.screen == ScreenConnections {
			cmd, shouldContinue := m.handleConnectionsKeyMsg(msg)
			if !shouldContinue {
				return m, cmd
			}
			// If we switched to main screen, start connecting
			if m.screen == ScreenMain && m.loading {
				// If SSH alias is set, establish tunnel first
				if m.sshAlias != "" {
					return m, establishSSHTunnel(m.sshAlias, m.connectionString)
				}
				// Direct connection - activeConnString is same as connectionString
				m.activeConnString = m.connectionString
				return m, connectToMongo(m.connectionString)
			}
			return m, cmd
		}

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
			cmd, handled := m.handleQueryKey(msg)
			if handled {
				return m, cmd
			}
		}

		// Handle collection search mode
		if m.collSearchActive {
			cmd, handled := m.handleCollectionSearchKey(msg)
			if handled {
				return m, cmd
			}
		}

		// Handle database search mode
		if m.dbSearchActive {
			cmd, handled := m.handleDatabaseSearchKey(msg)
			if handled {
				return m, cmd
			}
		}

		// Handle document search mode
		if m.docSearchActive {
			cmd, handled := m.handleDocSearchKey(msg)
			if handled {
				return m, cmd
			}
		}

		switch msg.String() {
		case "ctrl+c", "q":
			// Clean up SSH tunnel if active
			if m.sshTunnel != nil {
				m.sshTunnel.Close()
			}
			return m, tea.Quit

		case "/", "ctrl+s":
			// Activate search when on Databases, Collections, or Documents panel
			if m.focus == FocusDatabases {
				m.dbSearchActive = true
				m.dbSearchInput.Focus()
				m.updateFilteredDatabases()
				return m, textinput.Blink
			}
			if m.focus == FocusCollections {
				m.collSearchActive = true
				m.collSearchInput.Focus()
				m.updateFilteredCollections()
				return m, textinput.Blink
			}
			if m.focus == FocusDocuments {
				m.docSearchActive = true
				m.docSearchInput.SetValue("")
				m.docSearchInput.Focus()
				m.docSearchMatches = []int{}
				m.docSearchCurrent = -1
				return m, textinput.Blink
			}

		case "up", "k", "ctrl+p":
			switch m.focus {
			case FocusDatabases:
				if m.dbCursor > 0 {
					m.dbCursor--
					if len(m.dbFiltered) > 0 && m.client != nil {
						m.selectedDatabase = m.dbFiltered[m.dbCursor]
						m.explicitDBSelect = false // Arrow key = silent errors
						return m, loadCollections(m.client, m.selectedDatabase)
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
				if m.dbCursor < len(m.dbFiltered)-1 {
					m.dbCursor++
					if m.client != nil {
						m.selectedDatabase = m.dbFiltered[m.dbCursor]
						m.explicitDBSelect = false // Arrow key = silent errors
						return m, loadCollections(m.client, m.selectedDatabase)
					}
				}
			case FocusCollections:
				if m.collCursor < len(m.collFiltered)-1 {
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
				// Explicitly select database - show errors if load fails
				if len(m.dbFiltered) > 0 && m.client != nil {
					m.selectedDatabase = m.dbFiltered[m.dbCursor]
					m.explicitDBSelect = true
					m.focus = FocusCollections
					return m, loadCollections(m.client, m.selectedDatabase)
				}
				m.focus = FocusCollections
			case FocusCollections:
				if len(m.collFiltered) > 0 && m.client != nil && m.selectedDatabase != "" {
					m.selectedCollection = m.collFiltered[m.collCursor]
					m.loadingDocs = true
					m.docScrollOffset = 0
					m.docCursor = 0
					m.currentPage = 0
					m.queryFilter = bson.M{}
					m.queryText = "{}"
					m.queryCursor = 1
					m.focus = FocusDocuments
					return m, loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, 0, m.queryFilter)
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
					return m, loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, m.currentPage, m.queryFilter)
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
					return m, loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, m.currentPage, m.queryFilter)
				}
			}

		case "p":
			// Previous page of documents
			if m.focus == FocusDocuments && m.currentPage > 0 {
				m.currentPage--
				m.loadingDocs = true
				m.docCursor = 0
				m.docScrollOffset = 0
				return m, loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, m.currentPage, m.queryFilter)
			}

		case "P":
			// Jump to first page of documents
			if m.focus == FocusDocuments && m.currentPage > 0 {
				m.currentPage = 0
				m.loadingDocs = true
				m.docCursor = 0
				m.docScrollOffset = 0
				return m, loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, 0, m.queryFilter)
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
			m.errorModal = true
			m.errorMessage = fmt.Sprintf("Editor error: %v", msg.err)
			return m, nil
		}

		// Read the potentially modified file
		newJSON, err := os.ReadFile(msg.tempFile)
		if err != nil {
			m.errorModal = true
			m.errorMessage = fmt.Sprintf("Failed to read edited file: %v", err)
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
			m.errorModal = true
			m.errorMessage = fmt.Sprintf("Invalid JSON: %v\n\nDocument was NOT saved.", err)
			return m, nil
		}

		// Save to MongoDB
		return m, m.saveDocument(msg.docIndex, newDoc)

	case documentSavedMsg:
		if msg.err != nil {
			m.errorModal = true
			m.errorMessage = fmt.Sprintf("Failed to save document: %v\n\nDocument was NOT saved.", msg.err)
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
		m.updateFilteredDatabases()

		// Store client for later use - reconnect to get it
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, _ := mongo.Connect(ctx, options.Client().ApplyURI(m.activeConnString))
		m.client = client

		// Check if we should auto-select a database from DATABASE_NAME env var
		if m.autoSelectDB != "" {
			for i, db := range m.dbFiltered {
				if db == m.autoSelectDB {
					m.dbCursor = i
					m.selectedDatabase = db
					m.focus = FocusCollections // Shift focus to Collections panel
					m.autoSelectDB = ""        // Clear so we don't re-trigger
					return m, loadCollections(m.client, m.selectedDatabase)
				}
			}
			// Database not found, clear autoSelectDB and fall through to default behavior
			m.autoSelectDB = ""
		}

		// Don't auto-load collections - user may not have access to all databases
		// Just select the first database visually, user will press Enter to load collections
		if len(m.dbFiltered) > 0 {
			m.selectedDatabase = m.dbFiltered[0]
		}

	case collectionsLoadedMsg:
		if msg.err != nil {
			// Only show error if user explicitly selected the database (pressed Enter)
			// Silently swallow errors when just arrowing through the list
			if m.explicitDBSelect {
				m.errorModal = true
				m.errorMessage = fmt.Sprintf("Failed to load collections: %v", msg.err)
			}
			return m, nil
		}
		m.collections = msg.collections
		m.collCursor = 0
		// Reset search state
		m.collSearchActive = false
		m.collSearchInput.SetValue("")
		m.updateFilteredCollections()
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

	case connectionsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Merge saved connections with default localhost
		m.connections = defaultConnections
		for _, conn := range msg.connections {
			// Don't duplicate if name matches a default
			isDuplicate := false
			for _, def := range defaultConnections {
				if conn.Name == def.Name {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				m.connections = append(m.connections, conn)
			}
		}
		// Initialize filtered connections
		m.updateFilteredConnections()

		// If DATABASE_NAME env var is set, auto-connect using localhost
		if m.autoSelectDB != "" && len(m.connections) > 0 {
			// Use the first connection (localhost)
			conn := m.connections[0]
			m.connectionString = conn.ConnectionString
			m.sshAlias = conn.SSHAlias
			m.screen = ScreenMain
			m.loading = true
			if m.sshAlias != "" {
				return m, establishSSHTunnel(m.sshAlias, m.connectionString)
			}
			// Direct connection
			m.activeConnString = m.connectionString
			return m, connectToMongo(m.connectionString)
		}

	case sshTunnelEstablishedMsg:
		if msg.err != nil {
			m.loading = false
			m.err = msg.err
			return m, nil
		}
		// Store the tunnel and the tunneled connection string
		m.sshTunnel = msg.tunnel
		m.activeConnString = msg.connectionString
		return m, connectToMongo(msg.connectionString)
	}

	return m, nil
}

func (m Model) View() string {
	// Show connections screen first
	if m.screen == ScreenConnections {
		return m.renderConnectionsScreen()
	}

	if m.err != nil {
		// Wrap long error messages for readability
		errStr := fmt.Sprintf("%v", m.err)
		maxWidth := m.width - 4
		if maxWidth < 40 {
			maxWidth = 80
		}
		var wrappedErr string
		for len(errStr) > maxWidth {
			wrappedErr += errStr[:maxWidth] + "\n"
			errStr = errStr[maxWidth:]
		}
		wrappedErr += errStr
		return fmt.Sprintf("Error:\n%s\n\nPress q to quit.", wrappedErr)
	}

	if m.loading {
		if m.sshAlias != "" && m.sshTunnel == nil {
			return "Establishing SSH tunnel..."
		}
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
	dbPanel := m.renderDatabasePanel(leftPanelInnerHeight)
	collPanel := m.renderCollectionPanel(leftPanelInnerHeight)
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
		Render("↑/↓: navigate • /: search • ←/→/space: collapse/expand • n/p: next/prev page • e: edit • tab: switch • q: quit")

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
	return m.renderListWithSelection(items, cursor, focused, maxHeight, true)
}

func (m Model) renderListWithSelection(items []string, cursor int, focused bool, maxHeight int, showUnfocusedSelection bool) string {
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
		} else if i == cursor && showUnfocusedSelection {
			rendered += selectedUnfocusedStyle.Render(item) + "\n"
		} else {
			rendered += normalStyle.Render(item) + "\n"
		}
	}

	return rendered
}

func main() {
	defer closeDB()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}
