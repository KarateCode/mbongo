package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.mongodb.org/mongo-driver/bson"
)

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

// handleQueryKey handles keyboard input when query panel is focused
// Returns the command to run and whether the key was handled
func (m *Model) handleQueryKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit, true
	case "tab":
		m.focus = FocusDocuments
		return nil, true
	case "shift+tab":
		m.focus = FocusCollections
		return nil, true
	case "left", "ctrl+b":
		if m.queryCursor > 0 {
			m.queryCursor--
		}
		return nil, true
	case "right", "ctrl+f":
		if m.queryCursor < len(m.queryText) {
			m.queryCursor++
		}
		return nil, true
	case "ctrl+a":
		m.queryCursor = 0
		return nil, true
	case "ctrl+e":
		m.queryCursor = len(m.queryText)
		return nil, true
	case "backspace", "ctrl+h":
		if m.queryCursor > 0 {
			m.queryText = m.queryText[:m.queryCursor-1] + m.queryText[m.queryCursor:]
			m.queryCursor--
		}
		return nil, true
	case "delete", "ctrl+d":
		if m.queryCursor < len(m.queryText) {
			m.queryText = m.queryText[:m.queryCursor] + m.queryText[m.queryCursor+1:]
		}
		return nil, true
	case "ctrl+k":
		// Kill to end of line
		m.queryText = m.queryText[:m.queryCursor]
		return nil, true
	case "ctrl+u":
		// Kill to beginning of line
		m.queryText = m.queryText[m.queryCursor:]
		m.queryCursor = 0
		return nil, true
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
				return nil, true
			}
			m.queryFilter = filter
			m.queryLoading = true
			m.currentPage = 0
			m.docCursor = 0
			m.docScrollOffset = 0
			return tea.Batch(
				m.querySpinner.Tick,
				loadDocuments(m.client, m.selectedDatabase, m.selectedCollection, 0, filter),
			), true
		}
		return nil, true
	default:
		// Insert regular characters
		if len(msg.String()) == 1 {
			ch := msg.String()[0]
			if ch >= 32 && ch < 127 {
				m.queryText = m.queryText[:m.queryCursor] + string(ch) + m.queryText[m.queryCursor:]
				m.queryCursor++
			}
		}
		return nil, true
	}
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
