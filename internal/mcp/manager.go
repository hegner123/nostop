package mcp

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// ManagedServer holds the runtime state for a connected MCP server.
type ManagedServer struct {
	Name   string
	Config ServerConfig
	Client *Client
	Tools  []ToolInfo
	Status ServerStatus
	Error  error
}

// ServerManager manages the lifecycle of MCP server connections.
type ServerManager struct {
	servers map[string]*ManagedServer
	mu      sync.RWMutex

	// clientName and clientVersion are sent during the Initialize handshake.
	clientName    string
	clientVersion string
}

// NewServerManager creates a new ServerManager.
func NewServerManager(clientName, clientVersion string) *ServerManager {
	return &ServerManager{
		servers:       make(map[string]*ManagedServer),
		clientName:    clientName,
		clientVersion: clientVersion,
	}
}

// LoadConfig loads server definitions from an MCPConfig into the manager.
// Does not start any servers — call StartAll() or Start() after loading.
func (m *ServerManager) LoadConfig(cfg *MCPConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, sc := range cfg.MCPServers {
		if sc.Disabled {
			log.Printf("[mcp] server %q is disabled, skipping", name)
			continue
		}

		// Don't overwrite a running server.
		if existing, ok := m.servers[name]; ok && existing.Status == ServerReady {
			continue
		}

		m.servers[name] = &ManagedServer{
			Name:   name,
			Config: sc,
			Status: ServerStopped,
		}
	}
}

// StartAll starts all configured servers concurrently.
// Failures are collected but do not prevent other servers from starting.
// Failed servers are marked with ServerError status.
func (m *ServerManager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name, srv := range m.servers {
		if srv.Status == ServerStopped {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	if len(names) == 0 {
		return nil
	}

	g, gCtx := errgroup.WithContext(ctx)
	for _, name := range names {
		g.Go(func() error {
			if err := m.Start(gCtx, name); err != nil {
				log.Printf("[mcp] failed to start server %q: %v", name, err)
				// Don't return the error — we want other servers to continue.
				return nil
			}
			return nil
		})
	}

	return g.Wait()
}

// Start starts a single MCP server by name.
func (m *ServerManager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	srv, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %q not configured", name)
	}
	srv.Status = ServerStarting
	srv.Error = nil
	m.mu.Unlock()

	// Create transport based on config.
	transport, err := m.createTransport(srv.Config)
	if err != nil {
		m.setServerError(name, fmt.Errorf("transport creation failed: %w", err))
		return err
	}

	// Create client and perform Initialize handshake.
	client := NewClient(transport)

	initCtx, cancel := context.WithTimeout(ctx, srv.Config.ParseTimeout())
	defer cancel()

	info, err := client.Initialize(initCtx, m.clientName, m.clientVersion)
	if err != nil {
		transport.Close()
		m.setServerError(name, fmt.Errorf("initialize handshake failed: %w", err))
		return err
	}

	// List available tools.
	tools, err := client.ListTools(initCtx)
	if err != nil {
		transport.Close()
		m.setServerError(name, fmt.Errorf("tools/list failed: %w", err))
		return err
	}

	// Update server state.
	m.mu.Lock()
	srv.Client = client
	srv.Tools = tools
	srv.Status = ServerReady
	srv.Error = nil
	m.mu.Unlock()

	log.Printf("[mcp] server %q ready: %s v%s, %d tools",
		name, info.Name, info.Version, len(tools))

	return nil
}

// Stop stops a single MCP server and cleans up resources.
func (m *ServerManager) Stop(name string) error {
	m.mu.Lock()
	srv, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %q not found", name)
	}

	client := srv.Client
	srv.Client = nil
	srv.Tools = nil
	srv.Status = ServerStopped
	srv.Error = nil
	m.mu.Unlock()

	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return client.Shutdown(ctx)
	}
	return nil
}

// StopAll stops all running servers.
func (m *ServerManager) StopAll() error {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name, srv := range m.servers {
		if srv.Status == ServerReady || srv.Status == ServerStarting {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	var firstErr error
	for _, name := range names {
		if err := m.Stop(name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// AllTools returns the combined tool list from all ready servers.
func (m *ServerManager) AllTools() []ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []ToolInfo
	for _, srv := range m.servers {
		if srv.Status == ServerReady {
			tools = append(tools, srv.Tools...)
		}
	}
	return tools
}

// CallTool calls a tool on a specific server.
// If the server is in Error state, attempts one restart before failing.
func (m *ServerManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*ToolResult, error) {
	m.mu.RLock()
	srv, ok := m.servers[serverName]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("MCP server %q not found", serverName)
	}

	// If server is in error state, attempt one restart.
	if srv.Status == ServerError {
		m.mu.RUnlock()
		log.Printf("[mcp] server %q in error state, attempting restart", serverName)
		if err := m.Start(ctx, serverName); err != nil {
			return nil, fmt.Errorf("MCP server %q restart failed: %w", serverName, err)
		}
		m.mu.RLock()
		srv = m.servers[serverName]
	}

	if srv.Status != ServerReady || srv.Client == nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("MCP server %q is not ready (status: %s)", serverName, srv.Status)
	}

	client := srv.Client
	m.mu.RUnlock()

	// Call the tool with the configured timeout.
	callCtx, cancel := context.WithTimeout(ctx, srv.Config.ParseTimeout())
	defer cancel()

	result, err := client.CallTool(callCtx, toolName, args)
	if err != nil {
		// Mark server as error if the call fails (transport-level failure).
		m.setServerError(serverName, err)
		return nil, err
	}

	return result, nil
}

// Status returns the status of all configured servers.
func (m *ServerManager) Status() map[string]ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]ServerStatus, len(m.servers))
	for name, srv := range m.servers {
		status[name] = srv.Status
	}
	return status
}

// ServerInfo returns detailed info for a named server.
func (m *ServerManager) ServerInfo(name string) (*ManagedServer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv, ok := m.servers[name]
	if !ok {
		return nil, false
	}
	// Return a shallow copy to avoid holding the lock.
	cp := *srv
	return &cp, true
}

// ServerNames returns all configured server names in no particular order.
func (m *ServerManager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// setServerError marks a server as failed.
func (m *ServerManager) setServerError(name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if srv, ok := m.servers[name]; ok {
		srv.Status = ServerError
		srv.Error = err
		srv.Tools = nil
	}
}

// createTransport creates a Transport from a ServerConfig.
func (m *ServerManager) createTransport(sc ServerConfig) (Transport, error) {
	switch sc.TransportType() {
	case "stdio":
		var env []string
		for k, v := range sc.Env {
			env = append(env, k+"="+v)
		}
		return NewStdioTransport(sc.Command, sc.Args, env)

	case "http":
		return NewHTTPTransport(sc.URL, sc.Headers, sc.ParseTimeout()), nil

	default:
		return nil, fmt.Errorf("invalid transport type (need command or url)")
	}
}
