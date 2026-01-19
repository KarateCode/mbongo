#!/bin/zsh

# Import SSH connections ending in "-integration" into mbongo database
# Parses ~/.ssh/config, SSHs into each box to get $MONGO_CONNECTION_STRING

# Don't exit on error - we want to continue even if SSH fails
set +e

# Database path
DB_PATH="${HOME}/.config/mbongo/mbongo.db"

# Ensure database directory exists
mkdir -p "$(dirname "$DB_PATH")"

# Create table if it doesn't exist
sqlite3 "$DB_PATH" "
CREATE TABLE IF NOT EXISTS connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    connection_string TEXT NOT NULL,
    ssh_alias TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
"

# Function to convert alias to display name
# e.g., "bauer-stag-na-cf-integration" -> "Bauer Stag Na Cf Integration"
alias_to_name() {
    echo "$1" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2))}1'
}

# Counters
added=0
updated=0
skipped=0

# Parse SSH config for hosts ending in "-integration"
echo "Parsing ~/.ssh/config for *-integration hosts..."

grep -E "^Host\s+.*-integration$" ~/.ssh/config | awk '{print $2}' | while read -r ssh_alias; do
    echo ""
    echo "Processing: $ssh_alias ---"
    
    # Generate display name
    name=$(alias_to_name "$ssh_alias")
    echo "  Name: $name"
    
    # SSH into the box and get MONGO_CONNECTION_STRING
    # Note: </dev/null prevents SSH from consuming stdin (which breaks the while loop)
    echo "  SSHing to get MONGO_CONNECTION_STRING..."
    mongo_conn_string=$(ssh -n -o ConnectTimeout=10 -o BatchMode=yes -o StrictHostKeyChecking=accept-new "$ssh_alias" 'echo $MONGO_CONNECTION_STRING' 2>/dev/null)
    ssh_exit_code=$?

    echo "  mon conn string: $mongo_conn_string"
    
    if [[ $ssh_exit_code -ne 0 ]]; then
        echo "  Failed to SSH (exit code: $ssh_exit_code), skipping..."
        ((skipped++))
        continue
    fi
    
    if [[ -z "$mongo_conn_string" ]]; then
        echo "  MONGO_CONNECTION_STRING is empty, skipping..."
        ((skipped++))
        continue
    fi
    
    echo "  Connection string: ${mongo_conn_string:0:50}..."
    
    # Escape single quotes for SQL
    name_escaped="${name//\'/\'\'}"
    mongo_escaped="${mongo_conn_string//\'/\'\'}"
    ssh_alias_escaped="${ssh_alias//\'/\'\'}"
    
    # Check if already exists in database
    existing=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM connections WHERE ssh_alias = '$ssh_alias_escaped';")
    
    if [[ "$existing" -gt 0 ]]; then
        # Update existing entry
        sqlite3 "$DB_PATH" "
            UPDATE connections 
            SET name = '$name_escaped', 
                connection_string = '$mongo_escaped'
            WHERE ssh_alias = '$ssh_alias_escaped';
        "
        echo "  Updated in database!"
        ((updated++))
    else
        # Insert new entry
        sqlite3 "$DB_PATH" "
            INSERT INTO connections (name, connection_string, ssh_alias)
            VALUES ('$name_escaped', '$mongo_escaped', '$ssh_alias_escaped');
        "
        echo "  Added to database!"
        ((added++))
    fi
done

echo ""
echo "========================================"
echo "Summary: $added added, $updated updated, $skipped skipped"
echo "========================================"
echo ""
echo "Current connections in database:"
sqlite3 "$DB_PATH" "SELECT name, ssh_alias, substr(connection_string, 1, 50) || '...' FROM connections;"
