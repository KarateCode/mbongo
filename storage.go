package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

// getDBPath returns the path to the SQLite database file
func getDBPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	mbongoDir := filepath.Join(homeDir, ".config", "mbongo")
	if err := os.MkdirAll(mbongoDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(mbongoDir, "mbongo.db"), nil
}

// initDB initializes the SQLite database
func initDB() error {
	dbPath, err := getDBPath()
	if err != nil {
		return err
	}

	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}

	// Create connections table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			connection_string TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Migration: Add ssh_alias column if it doesn't exist
	_, err = db.Exec(`ALTER TABLE connections ADD COLUMN ssh_alias TEXT DEFAULT ''`)
	if err != nil {
		// Ignore error if column already exists
		if err.Error() != "duplicate column name: ssh_alias" {
			// Check if it's actually a "column already exists" error (SQLite varies)
			// We'll just ignore alter table errors since the column may exist
		}
	}

	return nil
}

// loadConnections loads all connections from the database
func loadConnections() ([]Connection, error) {
	rows, err := db.Query("SELECT name, connection_string, COALESCE(ssh_alias, '') FROM connections ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var connections []Connection
	for rows.Next() {
		var conn Connection
		if err := rows.Scan(&conn.Name, &conn.ConnectionString, &conn.SSHAlias); err != nil {
			return nil, err
		}
		connections = append(connections, conn)
	}

	return connections, rows.Err()
}

// saveConnection saves a new connection to the database
func saveConnection(conn Connection) error {
	_, err := db.Exec(
		"INSERT INTO connections (name, connection_string, ssh_alias) VALUES (?, ?, ?)",
		conn.Name, conn.ConnectionString, conn.SSHAlias,
	)
	return err
}

// deleteConnection deletes a connection from the database by name
func deleteConnection(name string) error {
	_, err := db.Exec("DELETE FROM connections WHERE name = ?", name)
	return err
}

// updateConnection updates an existing connection in the database
func updateConnection(oldName string, conn Connection) error {
	_, err := db.Exec(
		"UPDATE connections SET name = ?, connection_string = ?, ssh_alias = ? WHERE name = ?",
		conn.Name, conn.ConnectionString, conn.SSHAlias, oldName,
	)
	return err
}

// closeDB closes the database connection
func closeDB() {
	if db != nil {
		db.Close()
	}
}
