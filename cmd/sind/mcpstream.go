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
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/atomicfile"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
	"github.com/spf13/afero"
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
	// An unset variable in --host "$HOST" must not open the stream to the
	// network, which is what an empty host means to net.Listen.
	if host == "" {
		return cobratree.Usagef("--host must not be empty: give an address, or 0.0.0.0 for all interfaces")
	}
	if port < 0 || port > 65535 {
		return cobratree.InvalidValue("port", strconv.Itoa(port), "a port from 0 to 65535")
	}
	logLevel, _ := cmd.Flags().GetString("log-level")
	if err := checkMCPLogLevel(logLevel); err != nil {
		return err
	}
	if logLevel != "" {
		_ = start.Flags().Set("log-level", logLevel)
	}

	// Listen first: a stream that cannot have its port must not replace
	// the token file of the stream that has it.
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	defer func() { _ = ln.Close() }()

	token, tokenPath, err := mcpStreamToken()
	if err != nil {
		return err
	}
	handler, stop, err := mcpStreamHandler(ctx, cfg, start, token)
	if err != nil {
		return err
	}
	defer stop()

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	defer context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	})()

	printMCPStreamHint(cmd, ln.Addr(), tokenPath)
	if err := server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// printMCPStreamHint tells on stderr where the stream listens, at addr,
// and how a client authenticates. It names the token's file, never the
// token, which stderr may carry into a log. A stream that listens on all
// interfaces says so: the URL it prints for clients is localhost's.
func printMCPStreamHint(cmd *cobra.Command, addr net.Addr, tokenPath string) {
	w := cmd.ErrOrStderr()
	url := streamURL(addr)
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp.IP.IsUnspecified() {
		_, _ = fmt.Fprintf(w, "MCP server listening on %s (all interfaces)\n", url)
	} else {
		_, _ = fmt.Fprintf(w, "MCP server listening on %s\n", url)
	}
	if tokenPath != "" {
		_, _ = fmt.Fprintf(w, "Bearer token in %s (new at every start; set %s to keep one)\n", tokenPath, mcpTokenEnv)
	} else {
		_, _ = fmt.Fprintf(w, "Bearer token from %s\n", mcpTokenEnv)
	}
	_, _ = fmt.Fprintf(w, "Client config: {\"type\": \"http\", \"url\": %q, \"headers\": {\"Authorization\": \"Bearer <token>\"}}\n", url)
}

// mcpMinTokenLength is the shortest SIND_MCP_TOKEN the stream accepts: the
// token is the only thing between a network and the user's Docker access
// when --host opens the stream to it.
const mcpMinTokenLength = 16

// streamURL returns the URL a client connects to for the address the stream
// listens on. A wildcard address (--host 0.0.0.0) is not one a client can
// connect to, so it becomes localhost.
func streamURL(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsUnspecified() {
		return "http://" + addr.String() + "/"
	}
	return "http://" + net.JoinHostPort("localhost", strconv.Itoa(tcp.Port)) + "/"
}

// mcpStreamToken returns the bearer token the stream requires: the value of
// SIND_MCP_TOKEN or, when it is not set, a new random token (crypto/rand),
// which it writes to mcpTokenFile in sind's state directory, readable by
// the user alone, and whose path it returns.
func mcpStreamToken() (token, path string, err error) {
	if token := os.Getenv(mcpTokenEnv); token != "" {
		if len(token) < mcpMinTokenLength {
			return "", "", fmt.Errorf("%s: a token has at least %d characters, e.g. from openssl rand -hex 32", mcpTokenEnv, mcpMinTokenLength)
		}
		if strings.ContainsFunc(token, func(c rune) bool { return c <= ' ' || c > '~' }) {
			return "", "", fmt.Errorf("%s: a token is printable ASCII without spaces", mcpTokenEnv)
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
	path = filepath.Join(dir, mcpTokenFile)
	if err := atomicfile.Write(afero.NewOsFs(), path, []byte(token+"\n"), 0o600); err != nil {
		return "", "", fmt.Errorf("writing MCP token: %w", err)
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
	// A tool call that runs to completion after its client is gone
	// (mcpRunsToCompletion) must not hold up the shutdown of the stream.
	stopInner := func() {
		cancel()
		select {
		case <-served:
		case <-time.After(mcpShutdownTimeout):
		}
	}

	var relay toolProgressRelay
	client := mcp.NewClient(&mcp.Implementation{Name: "sind-mcp-stream"},
		&mcp.ClientOptions{ProgressNotificationHandler: relay.handle})
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
			params := &mcp.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments}
			if req.Params.GetProgressToken() != nil {
				token, forget := relay.add(req)
				defer forget()
				params.SetProgressToken(token)
			}
			return session.CallTool(ctx, params)
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
