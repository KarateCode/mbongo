package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.mongodb.org/mongo-driver/bson"
)

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
		coll := m.client.Database(m.selectedDatabase).Collection(m.selectedCollection)
		_, err := coll.ReplaceOne(ctx, bson.M{"_id": docID}, newDoc)
		if err != nil {
			return documentSavedMsg{err: err, docIndex: docIndex}
		}

		return documentSavedMsg{err: nil, docIndex: docIndex, newDoc: newDoc}
	}
}
