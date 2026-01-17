package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

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

// updateFilteredDatabases updates the filtered databases based on search input
func (m *Model) updateFilteredDatabases() {
	query := m.dbSearchInput.Value()
	if query == "" {
		// No filter - show all databases
		m.dbFiltered = m.databases
		m.dbFilteredIndices = make([]int, len(m.databases))
		for i := range m.databases {
			m.dbFilteredIndices[i] = i
		}
	} else {
		// Filter databases by fuzzy match
		m.dbFiltered = []string{}
		m.dbFilteredIndices = []int{}
		for i, db := range m.databases {
			if fuzzyMatch(query, db) {
				m.dbFiltered = append(m.dbFiltered, db)
				m.dbFilteredIndices = append(m.dbFilteredIndices, i)
			}
		}
	}
	// Reset cursor if out of bounds
	if m.dbCursor >= len(m.dbFiltered) {
		m.dbCursor = len(m.dbFiltered) - 1
	}
	if m.dbCursor < 0 {
		m.dbCursor = 0
	}
}

// handleDatabaseSearchKey handles keyboard input when database search is active
// Returns the updated model, a command to run, and whether the key was handled
func (m *Model) handleDatabaseSearchKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit, true
	case "esc", "ctrl+g":
		// Cancel search
		m.dbSearchActive = false
		m.dbSearchInput.Blur()
		m.dbSearchInput.SetValue("")
		m.updateFilteredDatabases()
		return nil, true
	case "ctrl+n", "down":
		// Move cursor down in filtered list
		if m.dbCursor < len(m.dbFiltered)-1 {
			m.dbCursor++
		}
		return nil, true
	case "ctrl+p", "up":
		// Move cursor up in filtered list
		if m.dbCursor > 0 {
			m.dbCursor--
		}
		return nil, true
	case "enter":
		// Select the highlighted database and load its collections
		if len(m.dbFiltered) > 0 && m.client != nil {
			m.selectedDatabase = m.dbFiltered[m.dbCursor]
			// Clear search
			m.dbSearchActive = false
			m.dbSearchInput.Blur()
			m.dbSearchInput.SetValue("")
			m.updateFilteredDatabases()
			// Set cursor to the selected database in the full list
			for i, db := range m.dbFiltered {
				if db == m.selectedDatabase {
					m.dbCursor = i
					break
				}
			}
			// Navigate to Collections panel
			m.focus = FocusCollections
			return loadCollections(m.client, m.selectedDatabase), true
		}
		return nil, true
	default:
		// Pass to textinput
		var cmd tea.Cmd
		prevValue := m.dbSearchInput.Value()
		m.dbSearchInput, cmd = m.dbSearchInput.Update(msg)
		// Reset cursor to top when search text changes
		if m.dbSearchInput.Value() != prevValue {
			m.dbCursor = 0
		}
		m.updateFilteredDatabases()
		return cmd, true
	}
}

// renderDatabasePanel renders the database panel with optional search bar
func (m Model) renderDatabasePanel(innerHeight int) string {
	var dbContent string
	dbListHeight := innerHeight - 3
	if m.dbSearchActive {
		// Show search input at top, reduce list height
		dbListHeight -= 1
		dbContent = m.dbSearchInput.View() + "\n" + m.renderList(m.dbFiltered, m.dbCursor, true, dbListHeight)
	} else {
		dbContent = m.renderList(m.dbFiltered, m.dbCursor, m.focus == FocusDatabases, dbListHeight)
	}
	return m.renderPanel("Databases", "", dbContent, m.focus == FocusDatabases || m.dbSearchActive, leftPanelWidth, innerHeight)
}

// newDatabaseSearchInput creates a new textinput for database search
func newDatabaseSearchInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "search..."
	ti.CharLimit = 100
	ti.Width = leftPanelWidth - 4
	return ti
}

// fuzzyMatch returns true if pattern fuzzy-matches target (case-insensitive)
// Spaces in pattern separate independent sub-patterns that must all match.
// Characters in each sub-pattern must appear in target in order, but not necessarily contiguous.
// Example: "cus gr" matches "CustomerGroup" because "cus" and "gr" both fuzzy-match.
func fuzzyMatch(pattern, target string) bool {
	target = strings.ToLower(target)

	// Split pattern by spaces into sub-patterns
	subPatterns := strings.Fields(strings.ToLower(pattern))
	if len(subPatterns) == 0 {
		return true
	}

	// Each sub-pattern must fuzzy-match the target
	for _, subPattern := range subPatterns {
		if !fuzzyMatchSingle(subPattern, target) {
			return false
		}
	}
	return true
}

// fuzzyMatchSingle checks if a single pattern fuzzy-matches target
func fuzzyMatchSingle(pattern, target string) bool {
	pIdx := 0
	for tIdx := 0; tIdx < len(target) && pIdx < len(pattern); tIdx++ {
		if target[tIdx] == pattern[pIdx] {
			pIdx++
		}
	}
	return pIdx == len(pattern)
}
