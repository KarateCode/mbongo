package main

import "github.com/charmbracelet/lipgloss"

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

	// Style for selected item when panel is not focused (dimmer)
	selectedUnfocusedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252")).
				Background(lipgloss.Color("236")).
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
