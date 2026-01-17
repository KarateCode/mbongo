package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Connection represents a saved MongoDB connection
type Connection struct {
	Name             string
	ConnectionString string
}

// Default connections list
var defaultConnections = []Connection{
	{
		Name:             "localhost",
		ConnectionString: "mongodb://localhost:27017",
	},
}

// renderConnectionsScreen renders the connections selection screen
func (m Model) renderConnectionsScreen() string {
	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1).
		Render("Select a Connection")

	// Render connection list
	var listContent string
	for i, conn := range m.connections {
		item := conn.Name
		if i == m.connCursor {
			listContent += selectedStyle.Render(item) + "\n"
		} else {
			listContent += normalStyle.Render(item) + "\n"
		}
	}

	// Help text
	help := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1).
		Render("↑/↓: navigate • enter: connect • c: new • e: edit • d: delete • q: quit")

	// Center everything
	content := lipgloss.JoinVertical(lipgloss.Left, title, "", listContent, help)

	baseScreen := lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		content,
	)

	// Overlay the modal if it's open
	if m.newConnModal {
		return m.renderNewConnectionModal(baseScreen)
	}

	if m.deleteConnModal {
		return m.renderDeleteConnectionModal(baseScreen)
	}

	return baseScreen
}

// renderNewConnectionModal renders the new connection modal overlay
func (m Model) renderNewConnectionModal(background string) string {
	modalWidth := 50

	// Labels
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		MarginBottom(0)

	// Input styling based on focus
	nameLabel := labelStyle.Render("Name:")
	connLabel := labelStyle.Render("Connection String:")

	// Build the form
	formContent := lipgloss.JoinVertical(lipgloss.Left,
		nameLabel,
		m.newConnNameInput.View(),
		"",
		connLabel,
		m.newConnStringInput.View(),
	)

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1).
		Italic(true)
	helpText := helpStyle.Render("tab: switch field • enter: save • esc: cancel")

	// Modal title based on whether we're editing or creating
	modalTitle := "New Connection"
	if m.editingConnIndex >= 0 {
		modalTitle = "Edit Connection"
	}

	modalContent := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render(modalTitle),
		"",
		formContent,
		helpText,
	)

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Width(modalWidth)

	modal := modalStyle.Render(modalContent)

	// Center the modal on screen
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

// renderDeleteConnectionModal renders the delete confirmation modal overlay
func (m Model) renderDeleteConnectionModal(background string) string {
	modalWidth := 45

	connName := ""
	if m.deleteConnIndex >= 0 && m.deleteConnIndex < len(m.connections) {
		connName = m.connections[m.deleteConnIndex].Name
	}

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Render("Delete connection \"" + connName + "\"?")

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1).
		Italic(true)
	helpText := helpStyle.Render("enter/y: confirm • esc/n: cancel")

	modalContent := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Render("Delete Connection"),
		"",
		message,
		helpText,
	)

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(1, 2).
		Width(modalWidth)

	modal := modalStyle.Render(modalContent)

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

// handleConnectionsKey handles keyboard input on the connections screen
// Returns (command, shouldContinue)
func (m *Model) handleConnectionsKey(key string) bool {
	// Handle modal first if it's open
	if m.newConnModal {
		return m.handleNewConnModalKey(key)
	}

	switch key {
	case "up", "k", "ctrl+p":
		if m.connCursor > 0 {
			m.connCursor--
		}
		return true
	case "down", "j", "ctrl+n":
		if m.connCursor < len(m.connections)-1 {
			m.connCursor++
		}
		return true
	case "enter":
		if len(m.connections) > 0 {
			// Set the selected connection string and move to main screen
			m.connectionString = m.connections[m.connCursor].ConnectionString
			m.screen = ScreenMain
			m.loading = true
		}
		return true
	case "c":
		// Open new connection modal
		m.newConnModal = true
		m.newConnFocusName = true
		m.newConnNameInput.SetValue("")
		m.newConnStringInput.SetValue("")
		m.newConnNameInput.Focus()
		m.newConnStringInput.Blur()
		return true
	case "q", "ctrl+c":
		return false // Signal to quit
	}
	return true
}

// handleNewConnModalKey handles keyboard input in the new connection modal
func (m *Model) handleNewConnModalKey(key string) bool {
	switch key {
	case "esc", "ctrl+g":
		// Close modal without saving
		m.newConnModal = false
		m.newConnNameInput.Blur()
		m.newConnStringInput.Blur()
		return true
	case "tab", "shift+tab":
		// Toggle focus between fields
		m.newConnFocusName = !m.newConnFocusName
		if m.newConnFocusName {
			m.newConnNameInput.Focus()
			m.newConnStringInput.Blur()
		} else {
			m.newConnNameInput.Blur()
			m.newConnStringInput.Focus()
		}
		return true
	case "enter":
		// Save the connection
		name := strings.TrimSpace(m.newConnNameInput.Value())
		connString := strings.TrimSpace(m.newConnStringInput.Value())
		if name != "" && connString != "" {
			conn := Connection{Name: name, ConnectionString: connString}
			// Save to database
			if err := saveConnection(conn); err == nil {
				// Add to list
				m.connections = append(m.connections, conn)
			}
			// Close modal
			m.newConnModal = false
			m.newConnNameInput.Blur()
			m.newConnStringInput.Blur()
		}
		return true
	case "ctrl+c":
		return false // Signal to quit
	default:
		// Pass input to the focused text field
		var cmd tea.Cmd
		if m.newConnFocusName {
			m.newConnNameInput, cmd = m.newConnNameInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		} else {
			m.newConnStringInput, cmd = m.newConnStringInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
		_ = cmd
		return true
	}
}

// We need to update handleConnectionsKey to also handle textinput updates properly
// This requires passing the full tea.KeyMsg instead of just the string
func (m *Model) handleConnectionsKeyMsg(msg tea.KeyMsg) (tea.Cmd, bool) {
	// Handle modals first if they're open
	if m.newConnModal {
		return m.handleNewConnModalKeyMsg(msg)
	}
	if m.deleteConnModal {
		return m.handleDeleteConnModalKeyMsg(msg)
	}

	switch msg.String() {
	case "up", "k", "ctrl+p":
		if m.connCursor > 0 {
			m.connCursor--
		}
		return nil, true
	case "down", "j", "ctrl+n":
		if m.connCursor < len(m.connections)-1 {
			m.connCursor++
		}
		return nil, true
	case "enter":
		if len(m.connections) > 0 {
			// Set the selected connection string and move to main screen
			m.connectionString = m.connections[m.connCursor].ConnectionString
			m.screen = ScreenMain
			m.loading = true
		}
		return nil, true
	case "c":
		// Open new connection modal
		m.newConnModal = true
		m.newConnFocusName = true
		m.newConnNameInput.SetValue("")
		m.newConnStringInput.SetValue("")
		m.newConnNameInput.Focus()
		m.newConnStringInput.Blur()
		m.editingConnIndex = -1 // Creating new, not editing
		m.editingConnOldName = ""
		return textinput.Blink, true
	case "e":
		// Edit selected connection (but not the first one - localhost)
		if m.connCursor > 0 && m.connCursor < len(m.connections) {
			conn := m.connections[m.connCursor]
			m.newConnModal = true
			m.newConnFocusName = true
			m.newConnNameInput.SetValue(conn.Name)
			m.newConnStringInput.SetValue(conn.ConnectionString)
			m.newConnNameInput.Focus()
			m.newConnStringInput.Blur()
			m.editingConnIndex = m.connCursor
			m.editingConnOldName = conn.Name
			return textinput.Blink, true
		}
		return nil, true
	case "d":
		// Delete selected connection (but not the first one - localhost)
		if m.connCursor > 0 && m.connCursor < len(m.connections) {
			m.deleteConnModal = true
			m.deleteConnIndex = m.connCursor
		}
		return nil, true
	case "q", "ctrl+c":
		return tea.Quit, false
	}
	return nil, true
}

// handleDeleteConnModalKeyMsg handles keyboard input in the delete confirmation modal
func (m *Model) handleDeleteConnModalKeyMsg(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "ctrl+g", "n":
		// Cancel deletion
		m.deleteConnModal = false
		m.deleteConnIndex = -1
		return nil, true
	case "enter", "y":
		// Confirm deletion
		if m.deleteConnIndex > 0 && m.deleteConnIndex < len(m.connections) {
			connName := m.connections[m.deleteConnIndex].Name
			// Delete from database
			if err := deleteConnection(connName); err == nil {
				// Remove from list
				m.connections = append(m.connections[:m.deleteConnIndex], m.connections[m.deleteConnIndex+1:]...)
				// Adjust cursor if needed
				if m.connCursor >= len(m.connections) {
					m.connCursor = len(m.connections) - 1
				}
			}
		}
		m.deleteConnModal = false
		m.deleteConnIndex = -1
		return nil, true
	case "ctrl+c":
		return tea.Quit, false
	}
	return nil, true
}

// handleNewConnModalKeyMsg handles keyboard input in the new connection modal
func (m *Model) handleNewConnModalKeyMsg(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "ctrl+g":
		// Close modal without saving
		m.newConnModal = false
		m.newConnNameInput.Blur()
		m.newConnStringInput.Blur()
		return nil, true
	case "tab", "shift+tab":
		// Toggle focus between fields
		m.newConnFocusName = !m.newConnFocusName
		if m.newConnFocusName {
			m.newConnNameInput.Focus()
			m.newConnStringInput.Blur()
		} else {
			m.newConnNameInput.Blur()
			m.newConnStringInput.Focus()
		}
		return nil, true
	case "enter":
		// Save the connection
		name := strings.TrimSpace(m.newConnNameInput.Value())
		connString := strings.TrimSpace(m.newConnStringInput.Value())
		if name != "" && connString != "" {
			conn := Connection{Name: name, ConnectionString: connString}
			if m.editingConnIndex >= 0 {
				// Update existing connection
				if err := updateConnection(m.editingConnOldName, conn); err == nil {
					m.connections[m.editingConnIndex] = conn
				}
			} else {
				// Save new connection to database
				if err := saveConnection(conn); err == nil {
					m.connections = append(m.connections, conn)
				}
			}
			// Close modal
			m.newConnModal = false
			m.newConnNameInput.Blur()
			m.newConnStringInput.Blur()
		}
		return nil, true
	case "ctrl+c":
		return tea.Quit, false
	default:
		// Pass input to the focused text field
		var cmd tea.Cmd
		if m.newConnFocusName {
			m.newConnNameInput, cmd = m.newConnNameInput.Update(msg)
		} else {
			m.newConnStringInput, cmd = m.newConnStringInput.Update(msg)
		}
		return cmd, true
	}
}
