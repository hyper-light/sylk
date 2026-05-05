package fabric

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/client"
	"github.com/adalundhe/sylk/core/mcp/transport"
)

// AttachInProcessSession registers an in-process MCP connection that the
// catalog cannot describe in YAML. Used by tests, by Sylk-as-server (when
// an internal agent drives Sylk's own MCP server through the same wire
// without paying for stdio framing), and by integration with embedded MCP
// servers that ship in-process.
//
// The supplied transport must be the *client* end of an in-process pair;
// the matching server end is owned by whoever passed the transport in.
//
// Behaviour mirrors the catalog-driven path: handshake, capability sync,
// compile-and-register every tool/prompt/resource into the registry.
func (f *Fabric) AttachInProcessSession(ctx context.Context, serverID string, tx transport.Transport) error {
	if f == nil || f.connections == nil {
		return errors.New("mcp fabric: not initialized")
	}
	if tx == nil {
		return errors.New("mcp fabric: in-process transport is required")
	}
	if serverID == "" {
		return errors.New("mcp fabric: server id is required")
	}
	scope := concurrency.NewGoroutineScope(context.Background(), "mcp.connection."+safeID(serverID), nil)
	session, err := f.openInProcessSession(ctx, serverID, tx, scope)
	if err != nil {
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return err
	}
	syncCtx, cancel := context.WithTimeout(ctx, capabilitySyncDeadline())
	defer cancel()
	snapshot, err := session.SyncAll(syncCtx)
	if err != nil {
		_ = session.Close()
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return fmt.Errorf("mcp fabric: %s: capability sync: %w", serverID, err)
	}
	spec := catalog.ServerSpec{ID: serverID, Transport: catalog.TransportInProcess}
	state, err := f.connections.compileAndRegister(spec, tx, session, snapshot, scope)
	if err != nil {
		_ = session.Close()
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return err
	}
	f.connections.mu.Lock()
	f.connections.connections[serverID] = state
	f.connections.mu.Unlock()
	f.connections.recordDialSuccess(state)
	return nil
}

func (f *Fabric) openInProcessSession(
	ctx context.Context,
	serverID string,
	tx transport.Transport,
	scope *concurrency.GoroutineScope,
) (*client.Session, error) {
	sess, err := client.NewSession(client.SessionConfig{
		ServerID:           serverID,
		Transport:          tx,
		Scope:              scope,
		ClientInfo:         FabricImplementation,
		ClientCapabilities: mcp.ClientCapabilities{Roots: &mcp.RootsCapability{ListChanged: true}},
	})
	if err != nil {
		return nil, err
	}
	if err := sess.Start(ctx); err != nil {
		return nil, err
	}
	return sess, nil
}
