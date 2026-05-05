package fabric

import (
	"fmt"
	"net/http"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/transport"
)

// buildTransport materializes a transport.Transport from a catalog spec.
// Centralizes the spec→transport mapping so the connection manager doesn't
// switch on transport kind.
func buildTransport(spec catalog.ServerSpec, scope *concurrency.GoroutineScope) (transport.Transport, error) {
	switch spec.Transport {
	case catalog.TransportStdio:
		return buildStdio(spec, scope)
	case catalog.TransportHTTP, catalog.TransportSSE:
		return buildHTTP(spec, scope)
	case catalog.TransportInProcess:
		return nil, fmt.Errorf("mcp transport: inprocess servers are wired by the fabric, not the catalog")
	}
	return nil, fmt.Errorf("mcp transport: unsupported kind %q", spec.Transport)
}

func buildStdio(spec catalog.ServerSpec, scope *concurrency.GoroutineScope) (transport.Transport, error) {
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	return transport.NewStdio(transport.StdioConfig{
		Command: spec.Command,
		Args:    append([]string(nil), spec.Args...),
		Env:     env,
		WorkDir: spec.WorkDir,
		Scope:   scope,
	})
}

func buildHTTP(spec catalog.ServerSpec, scope *concurrency.GoroutineScope) (transport.Transport, error) {
	headers := http.Header{}
	for k, v := range spec.Headers {
		headers.Set(k, v)
	}
	return transport.NewHTTP(transport.HTTPConfig{
		Endpoint: spec.Endpoint,
		Headers:  headers,
		Scope:    scope,
	})
}
