package main

import "go.mongodb.org/mongo-driver/bson"

// Messages for async operations

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

// sshTunnelEstablishedMsg is sent when an SSH tunnel is established
type sshTunnelEstablishedMsg struct {
	tunnel           *SSHTunnel
	connectionString string // Modified connection string pointing to local tunnel
	err              error
}
