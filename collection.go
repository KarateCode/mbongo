package main

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"go.mongodb.org/mongo-driver/bson"
)

// updateFilteredCollections updates the filtered collections based on search input
func (m *Model) updateFilteredCollections() {
	query := m.collSearchInput.Value()
	if query == "" {
		// No filter - show all collections
		m.collFiltered = m.collections
		m.collFilteredIndices = make([]int, len(m.collections))
		for i := range m.collections {
			m.collFilteredIndices[i] = i
		}
	} else {
		// Filter collections by fuzzy match
		m.collFiltered = []string{}
		m.collFilteredIndices = []int{}
		for i, coll := range m.collections {
			if fuzzyMatch(query, coll) {
				m.collFiltered = append(m.collFiltered, coll)
				m.collFilteredIndices = append(m.collFilteredIndices, i)
			}
		}
	}
	// Reset cursor if out of bounds
	if m.collCursor >= len(m.collFiltered) {
		m.collCursor = len(m.collFiltered) - 1
	}
	if m.collCursor < 0 {
		m.collCursor = 0
	}
}

// handleCollectionSearchKey handles keyboard input when collection search is active
// Returns the updated model, a command to run, and whether the key was handled
func (m *Model) handleCollectionSearchKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit, true
	case "esc", "ctrl+g":
		// Cancel search
		m.collSearchActive = false
		m.collSearchInput.Blur()
		m.collSearchInput.SetValue("")
		m.updateFilteredCollections()
		return nil, true
	case "ctrl+n", "down":
		// Move cursor down in filtered list
		if m.collCursor < len(m.collFiltered)-1 {
			m.collCursor++
		}
		return nil, true
	case "ctrl+p", "up":
		// Move cursor up in filtered list
		if m.collCursor > 0 {
			m.collCursor--
		}
		return nil, true
	case "enter":
		// Select the highlighted collection
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
			// Clear search
			m.collSearchActive = false
			m.collSearchInput.Blur()
			m.collSearchInput.SetValue("")
			m.updateFilteredCollections()
			// Set cursor to the selected collection in the full list
			for i, coll := range m.collFiltered {
				if coll == m.selectedCollection {
					m.collCursor = i
					break
				}
			}
			return loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, 0, m.queryFilter), true
		}
		return nil, true
	default:
		// Pass to textinput
		var cmd tea.Cmd
		prevValue := m.collSearchInput.Value()
		m.collSearchInput, cmd = m.collSearchInput.Update(msg)
		// Reset cursor to top when search text changes
		if m.collSearchInput.Value() != prevValue {
			m.collCursor = 0
		}
		m.updateFilteredCollections()
		return cmd, true
	}
}

// renderCollectionPanel renders the collection panel with optional search bar
func (m Model) renderCollectionPanel(innerHeight int) string {
	var collContent string
	collListHeight := innerHeight - 3
	if m.collSearchActive {
		// Show search input at top, reduce list height
		collListHeight -= 1
		collContent = m.collSearchInput.View() + "\n" + m.renderList(m.collFiltered, m.collCursor, true, collListHeight)
	} else {
		collContent = m.renderList(m.collFiltered, m.collCursor, m.focus == FocusCollections, collListHeight)
	}
	return m.renderPanel("Collections", "", collContent, m.focus == FocusCollections || m.collSearchActive, leftPanelWidth, innerHeight)
}

// newCollectionSearchInput creates a new textinput for collection search
func newCollectionSearchInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "search..."
	ti.CharLimit = 100
	ti.Width = leftPanelWidth - 4
	return ti
}
