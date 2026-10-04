// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
)

// mcpTokenEnv names the environment variable that sets the bearer token
// `sind mcp stream` requires.
const mcpTokenEnv = "SIND_MCP_TOKEN"

// mcpTokenFile is the file in sind's state directory that holds the token
// `sind mcp stream` generated when mcpTokenEnv is not set.
const mcpTokenFile = "mcp-token"

// mcpShutdownTimeout bounds how long a stopped stream waits for requests
// in flight.
const mcpShutdownTimeout = 5 * time.Second

// runMCPStream serves sind's MCP tools over streamable HTTP, on --host and
// --port, to clients that send the bearer token from mcpStreamToken.
//
// Binding to loopback keeps other hosts out, not the other users of this
// one, and every tool runs sind with the Docker access of the user who
// started the stream: without the token, any local account could create a
// worker from its own image with every capability. ophis's own stream has
// no hook for authentication, so the stream is sind's: an HTTP server
// whose MCP server forwards each tool call to ophis's server (mcpStreamHandler).
//
// It stops cleanly, exiting 0, when the context ends (SIGINT or SIGTERM).
func runMCPStream(cmd *cobra.Command, cfg *ophis.Config, start *cobra.Command) error {
	ctx := cmd.Context()
	host, _ := cmd.Flags().GetString("host")
	port, _ := cmd.Flags().GetInt("port")
	if logLevel, _ := cmd.Flags().GetString("log-level"); logLevel != "" {
		_ = start.Flags().Set("log-level", logLevel)
	}

	token, tokenPath, err := mcpStreamToken()
	if err != nil {
		return err
	}
	handler, stop, err := mcpStreamHandler(ctx, cfg, start, token)
	if err != nil {
		return err
	}
	defer stop()

	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan struct{})
	defer close(served)
	go func() {
		select {
		case <-ctx.Done():
		case <-served:
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	printMCPStreamHint(cmd, "http://"+ln.Addr().String()+"/", tokenPath)
	if err := server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// printMCPStreamHint tells on stderr where the stream listens and how a
// client authenticates. It names the token's file, never the token, which
// stderr may carry into a log.
func printMCPStreamHint(cmd *cobra.Command, url, tokenPath string) {
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "MCP server listening on %s\n", url)
	if tokenPath != "" {
		_, _ = fmt.Fprintf(w, "Bearer token in %s (new at every start; set %s to keep one)\n", tokenPath, mcpTokenEnv)
	} else {
		_, _ = fmt.Fprintf(w, "Bearer token from %s\n", mcpTokenEnv)
	}
	_, _ = fmt.Fprintf(w, "Client config: {\"type\": \"http\", \"url\": %q, \"headers\": {\"Authorization\": \"Bearer <token>\"}}\n", url)
}

// mcpStreamToken returns the bearer token the stream requires: the value of
// SIND_MCP_TOKEN or, when it is not set, a new random token (crypto/rand),
// which it writes to mcpTokenFile in sind's state directory, readable by
// the user alone, and whose path it returns.
func mcpStreamToken() (token, path string, err error) {
	if token := os.Getenv(mcpTokenEnv); token != "" {
		for _, c := range []byte(token) {
			if c <= ' ' || c > '~' {
				return "", "", fmt.Errorf("%s: a token is printable ASCII without spaces", mcpTokenEnv)
			}
		}
		return token, "", nil
	}

	dir, err := state.Dir()
	if err != nil {
		return "", "", fmt.Errorf("resolving state directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("creating state directory: %w", err)
	}
	token = rand.Text()
	// A new file, renamed into place, is 0600 even if an older token file
	// was not.
	f, err := os.CreateTemp(dir, "."+mcpTokenFile+"-*")
	if err != nil {
		return "", "", fmt.Errorf("writing MCP token: %w", err)
	}
	_, werr := f.WriteString(token + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	path = filepath.Join(dir, mcpTokenFile)
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", "", fmt.Errorf("writing MCP token: %w", werr)
	}
	return token, path, nil
}

// mcpStreamHandler returns the stream's HTTP handler, which requires the
// bearer token, and the function that stops what serves it.
//
// ophis keeps the MCP server it builds to itself, so `sind mcp start` runs
// it here on an in-memory transport (ophis.Config.Transport), and an MCP
// client session on the other end lists its tools. A second MCP server
// offers those tools over HTTP and forwards every call through the
// session: ophis still builds the tools and runs every call, with the
// selectors and middlewares of mcpConfig.
func mcpStreamHandler(ctx context.Context, cfg *ophis.Config, start *cobra.Command, token string) (http.Handler, func(), error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	cfg.Transport = serverTransport
	innerCtx, cancel := context.WithCancel(ctx)
	start.SetContext(innerCtx)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = start.RunE(start, nil)
	}()
	stopInner := func() {
		cancel()
		<-served
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "sind-mcp-stream"}, nil)
	session, err := client.Connect(innerCtx, clientTransport, nil)
	if err != nil {
		stopInner()
		return nil, nil, fmt.Errorf("starting the MCP server: %w", err)
	}
	stop := func() {
		_ = session.Close()
		stopInner()
	}

	info := *session.InitializeResult().ServerInfo
	server := mcp.NewServer(&info, nil)
	for tool, err := range session.Tools(innerCtx, nil) {
		if err != nil {
			stop()
			return nil, nil, fmt.Errorf("listing MCP tools: %w", err)
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return session.CallTool(ctx, &mcp.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments})
		})
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return requireBearerToken(token, handler), stop, nil
}

// requireBearerToken returns h behind go-sdk's bearer token check, which
// answers 401 to a request without "Authorization: Bearer <token>".
func requireBearerToken(token string, h http.Handler) http.Handler {
	verify := func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{}, nil
	}
	return auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
}
