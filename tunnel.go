package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
)

// SSHTunnel represents an active SSH tunnel
type SSHTunnel struct {
	sshClient  *ssh.Client
	listener   net.Listener
	localAddr  string // The local address to connect to (e.g., "localhost:27018")
	remoteAddr string // The remote MongoDB address
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewSSHTunnel creates and starts an SSH tunnel using an alias from ~/.ssh/config
func NewSSHTunnel(sshAlias, remoteMongoAddr string) (*SSHTunnel, error) {
	// Parse SSH config to get connection details
	configPath := filepath.Join(os.Getenv("HOME"), ".ssh", "config")
	configFile, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SSH config: %w", err)
	}
	defer configFile.Close()

	cfg, err := ssh_config.Decode(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH config: %w", err)
	}

	// Get connection details from config
	hostname, _ := cfg.Get(sshAlias, "HostName")
	if hostname == "" {
		hostname = sshAlias // Use alias as hostname if not specified
	}

	user, _ := cfg.Get(sshAlias, "User")
	if user == "" {
		user = os.Getenv("USER")
	}

	portStr, _ := cfg.Get(sshAlias, "Port")
	if portStr == "" {
		portStr = "22"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 22
	}

	identityFile, _ := cfg.Get(sshAlias, "IdentityFile")
	if identityFile == "" {
		identityFile = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
	}
	// Expand ~ in path
	if strings.HasPrefix(identityFile, "~/") {
		identityFile = filepath.Join(os.Getenv("HOME"), identityFile[2:])
	}

	// Read private key
	keyBytes, err := os.ReadFile(identityFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key %s: %w", identityFile, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH key: %w", err)
	}

	// Create SSH client config
	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: Use known_hosts
	}

	// Connect to SSH server
	sshAddr := fmt.Sprintf("%s:%d", hostname, port)
	sshClient, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH server %s: %w", sshAddr, err)
	}

	// Start local listener on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("failed to start local listener: %w", err)
	}

	tunnel := &SSHTunnel{
		sshClient:  sshClient,
		listener:   listener,
		localAddr:  listener.Addr().String(),
		remoteAddr: remoteMongoAddr,
		done:       make(chan struct{}),
	}

	// Start accepting connections
	tunnel.wg.Add(1)
	go tunnel.acceptLoop()

	return tunnel, nil
}

// LocalAddr returns the local address to connect to
func (t *SSHTunnel) LocalAddr() string {
	return t.localAddr
}

// Close shuts down the tunnel
func (t *SSHTunnel) Close() error {
	close(t.done)
	t.listener.Close()
	t.wg.Wait()
	return t.sshClient.Close()
}

func (t *SSHTunnel) acceptLoop() {
	defer t.wg.Done()

	for {
		localConn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
				continue
			}
		}

		t.wg.Add(1)
		go t.handleConnection(localConn)
	}
}

func (t *SSHTunnel) handleConnection(localConn net.Conn) {
	defer t.wg.Done()
	defer localConn.Close()

	// Connect to remote MongoDB through SSH
	remoteConn, err := t.sshClient.Dial("tcp", t.remoteAddr)
	if err != nil {
		return
	}
	defer remoteConn.Close()

	// Bidirectional copy
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(remoteConn, localConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(localConn, remoteConn)
		done <- struct{}{}
	}()

	// Wait for either direction to finish or tunnel to close
	select {
	case <-done:
	case <-t.done:
	}
}

// ParseMongoHostPort extracts host:port from a MongoDB connection string
func ParseMongoHostPort(connStr string) string {
	// Remove mongodb:// prefix
	s := strings.TrimPrefix(connStr, "mongodb://")
	s = strings.TrimPrefix(s, "mongodb+srv://")

	// Remove credentials if present (user:pass@)
	if idx := strings.Index(s, "@"); idx != -1 {
		s = s[idx+1:]
	}

	// Remove database and options (/dbname?options)
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}

	// Remove options without database (?options)
	if idx := strings.Index(s, "?"); idx != -1 {
		s = s[:idx]
	}

	// If no port specified, add default MongoDB port
	if !strings.Contains(s, ":") {
		s = s + ":27017"
	}

	return s
}

// BuildTunneledConnectionString creates a connection string pointing to the local tunnel
func BuildTunneledConnectionString(originalConnStr, localAddr string) string {
	// Replace the host:port in the connection string with the tunnel's local address
	s := originalConnStr

	// Handle mongodb:// prefix
	prefix := ""
	if strings.HasPrefix(s, "mongodb://") {
		prefix = "mongodb://"
		s = strings.TrimPrefix(s, "mongodb://")
	} else if strings.HasPrefix(s, "mongodb+srv://") {
		// SRV connections need to be converted to regular mongodb://
		prefix = "mongodb://"
		s = strings.TrimPrefix(s, "mongodb+srv://")
	}

	// Preserve credentials if present
	credentials := ""
	if idx := strings.Index(s, "@"); idx != -1 {
		credentials = s[:idx+1]
		s = s[idx+1:]
	}

	// Preserve database and options
	suffix := ""
	if idx := strings.Index(s, "/"); idx != -1 {
		suffix = s[idx:]
	} else if idx := strings.Index(s, "?"); idx != -1 {
		suffix = s[idx:]
	}

	result := prefix + credentials + localAddr + suffix

	// Auto-append directConnection=true for tunneled connections
	// This prevents the driver from trying to connect to replica set members
	// using their internal hostnames which aren't accessible through the tunnel
	if strings.Contains(result, "?") {
		// Already has query params, append with &
		if !strings.Contains(strings.ToLower(result), "directconnection=") {
			result += "&directConnection=true"
		}
	} else if strings.Contains(result, "/") {
		// Has database but no query params
		if !strings.Contains(strings.ToLower(result), "directconnection=") {
			result += "?directConnection=true"
		}
	} else {
		// No database or query params
		result += "/?directConnection=true"
	}

	return result
}
