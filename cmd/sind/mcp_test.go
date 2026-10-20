// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPStream_ListensOnLocalhost(t *testing.T) {
	stream, _, err := NewRootCommand().Find([]string{"mcp", "stream"})
	require.NoError(t, err)

	host := stream.Flags().Lookup("host")
	require.NotNil(t, host)
	assert.Equal(t, "127.0.0.1", host.DefValue)
	assert.Equal(t, "127.0.0.1", host.Value.String())
	assert.Equal(t, "8080", stream.Flags().Lookup("port").DefValue)
}

// mcpTool is the part of an exported MCP tool definition the tests look at.
type mcpTool struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties struct {
			Flags struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"flags"`
		} `json:"properties"`
	} `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

// exportMCPTools runs `sind mcp tools` in a temporary directory and returns
// the tools it exported, by name.
func exportMCPTools(t *testing.T) map[string]mcpTool {
	t.Helper()
	t.Chdir(t.TempDir())
	_, _, err := executeCommand("mcp", "tools")
	require.NoError(t, err)
	data, err := os.ReadFile("mcp-tools.json")
	require.NoError(t, err)
	var list []mcpTool
	require.NoError(t, json.Unmarshal(data, &list))
	tools := make(map[string]mcpTool, len(list))
	for _, tool := range list {
		tools[tool.Name] = tool
	}
	return tools
}

func TestMCPTools_Set(t *testing.T) {
	tools := exportMCPTools(t)
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	slices.Sort(names)
	assert.Equal(t, []string{
		"sind_connect_container",
		"sind_create_cluster",
		"sind_create_worker",
		"sind_delete_cluster",
		"sind_delete_realm",
		"sind_delete_worker",
		"sind_disconnect_container",
		"sind_doctor",
		"sind_exec",
		"sind_get_cluster",
		"sind_get_clusters",
		"sind_get_coredns-config",
		"sind_get_dns",
		"sind_get_mesh",
		"sind_get_monitoring",
		"sind_get_networks",
		"sind_get_node",
		"sind_get_nodes",
		"sind_get_realms",
		"sind_get_ssh-config",
		"sind_get_ssh-known-hosts",
		"sind_get_ssh-public-key",
		"sind_get_volumes",
		"sind_logs",
		"sind_power_cut",
		"sind_power_cycle",
		"sind_power_freeze",
		"sind_power_on",
		"sind_power_reboot",
		"sind_power_shutdown",
		"sind_power_unfreeze",
		"sind_version",
	}, names)
}

func TestMCPExcluded_NamesExistingCommands(t *testing.T) {
	root := NewRootCommand()
	for path := range mcpExcluded {
		cmd, rest, err := root.Find(strings.Fields(path))
		require.NoError(t, err, path)
		assert.Empty(t, rest, path)
		assert.Equal(t, path, commandPath(cmd))
	}
}

func TestMCPTools_Flags(t *testing.T) {
	tools := exportMCPTools(t)
	for name, tool := range tools {
		flags := tool.InputSchema.Properties.Flags.Properties
		assert.NotContains(t, flags, "verbose", name)
		assert.NotContains(t, flags, "follow", name)
		assert.Contains(t, flags, "realm", name)
	}
	assert.Contains(t, tools["sind_create_cluster"].InputSchema.Properties.Flags.Properties, "config")
}

// TestMCPTools_NoDefault checks that a flag that means something else when
// it is not given than with its default value has no default in the input
// schema, which a client would fill in.
func TestMCPTools_NoDefault(t *testing.T) {
	tools := exportMCPTools(t)
	for path, names := range mcpNoDefault {
		flags := tools[mcpToolName(path)].InputSchema.Properties.Flags.Properties
		for _, name := range names {
			require.Contains(t, flags, name, path)
			var schema map[string]any
			require.NoError(t, json.Unmarshal(flags[name], &schema))
			assert.NotContains(t, schema, "default", path+" --"+name)
		}
	}
	var count map[string]any
	require.NoError(t, json.Unmarshal(tools["sind_create_worker"].InputSchema.Properties.Flags.Properties["count"], &count))
	assert.InDelta(t, 1, count["default"], 0, "other flags keep their default")

	logs, _, err := NewRootCommand().Find([]string{"logs"})
	require.NoError(t, err)
	assert.Equal(t, "0", logs.Flags().Lookup("tail").DefValue, "the command keeps its default")
}

// TestMCPFlags_NameExistingFlags checks that mcpNoDefault names flags that
// exist.
func TestMCPFlags_NameExistingFlags(t *testing.T) {
	root := NewRootCommand()
	check := func(path, name string) {
		cmd, _, err := root.Find(strings.Fields(path))
		require.NoError(t, err, path)
		require.Equal(t, path, commandPath(cmd))
		assert.NotNil(t, cmd.Flags().Lookup(name), path+" --"+name)
	}
	for path, names := range mcpNoDefault {
		for _, name := range names {
			check(path, name)
		}
	}
}

func TestMCPTools_NoOutputFlag(t *testing.T) {
	tools := exportMCPTools(t)
	var get int
	for name, tool := range tools {
		if strings.HasPrefix(name, "sind_get_") {
			get++
			assert.NotContains(t, tool.InputSchema.Properties.Flags.Properties, "output", name)
		}
	}
	assert.Equal(t, 14, get)
	assert.NotContains(t, tools["sind_doctor"].InputSchema.Properties.Flags.Properties, "output")
}

func TestForceJSONOutput(t *testing.T) {
	in := ophis.ToolInput{Flags: map[string]any{"output": "human", "realm": "ci-42"}, Args: []string{"dev"}}
	var got ophis.ToolInput
	next := func(_ context.Context, _ *mcp.CallToolRequest, in ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		got = in
		return nil, ophis.ToolOutput{}, nil
	}

	_, _, err := forceJSONOutput(t.Context(), &mcp.CallToolRequest{}, in, next)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"output": "json", "realm": "ci-42"}, got.Flags)
	assert.Equal(t, []string{"dev"}, got.Args)
	assert.Equal(t, "human", in.Flags["output"], "the caller's map is not modified")

	_, _, err = forceJSONOutput(t.Context(), &mcp.CallToolRequest{}, ophis.ToolInput{}, next)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"output": "json"}, got.Flags)
}

// TestMCPTools_Hints checks that every tool says whether it is read-only
// and, if not, whether it is destructive. A new command has to be added to
// mcpEffects.
func TestMCPTools_Hints(t *testing.T) {
	tools := exportMCPTools(t)
	for name, tool := range tools {
		// go-sdk leaves readOnlyHint out when it is false, but always
		// writes destructiveHint when it is set.
		readOnly := tool.Annotations["readOnlyHint"] == true
		_, hasDestructive := tool.Annotations["destructiveHint"]
		assert.True(t, readOnly != hasDestructive,
			"%s must be read-only or say whether it is destructive; add it to mcpEffects", name)
	}
	assert.Equal(t, true, tools["sind_get_nodes"].Annotations["readOnlyHint"])
	assert.Equal(t, true, tools["sind_create_cluster"].Annotations["destructiveHint"])
	assert.Equal(t, true, tools["sind_create_worker"].Annotations["destructiveHint"])
	assert.Equal(t, false, tools["sind_power_on"].Annotations["destructiveHint"])
	assert.Equal(t, true, tools["sind_delete_cluster"].Annotations["destructiveHint"])
	assert.Equal(t, true, tools["sind_exec"].Annotations["destructiveHint"])
}

func TestMCPEffects_NameTools(t *testing.T) {
	root := NewRootCommand()
	for path := range mcpEffects {
		cmd, rest, err := root.Find(strings.Fields(path))
		require.NoError(t, err, path)
		assert.Empty(t, rest, path)
		assert.True(t, isMCPTool(cmd), "%s is not an MCP tool", path)
	}
}

// TestMCPServer_VersionAndTools runs `sind mcp start` over an in-memory
// transport and checks what a client sees: sind's version and the tools
// with their hints.
func TestMCPServer_VersionAndTools(t *testing.T) {
	setVersion(t, "v1.2.3", "")
	// The server sets it for its tool calls, in this process.
	t.Setenv(envLogTimestamps, "")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	cfg := mcpConfig()
	cfg.Transport = serverTransport

	root := NewRootCommand()
	old, _, err := root.Find([]string{"mcp"})
	require.NoError(t, err)
	root.RemoveCommand(old)
	root.AddCommand(newMCPCommand(cfg))
	root.SetArgs([]string{"mcp", "start"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The server ends with nil or context.Canceled, whichever of the
	// session's close and the cancel it sees first.
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = root.ExecuteContext(ctx)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	info := session.InitializeResult().ServerInfo
	assert.Equal(t, "sind", info.Name)
	assert.Equal(t, "1.2.3", info.Version)
	assert.Empty(t, root.Flags().Lookup("version"), "the root has no --version flag")

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, tools.Tools, 32)
	for _, tool := range tools.Tools {
		require.NotNil(t, tool.Annotations, tool.Name)
	}

	require.NoError(t, session.Close())
	cancel()
	<-served
}

func TestRefuseFlagArgs(t *testing.T) {
	var called ophis.ToolInput
	run := func(_ context.Context, _ *mcp.CallToolRequest, in ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		called = in
		return nil, ophis.ToolOutput{}, nil
	}
	// refused reports whether the call was refused, as a usage error
	// with the exit status and the error line of one, without running.
	refused := func(mw ophis.MiddlewareFunc, tool string, args ...string) bool {
		called = ophis.ToolInput{}
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool}}
		res, out, err := mw(t.Context(), req, ophis.ToolInput{Args: args}, run)
		require.NoError(t, err, args)
		if out.ExitCode == 0 {
			assert.NotNil(t, called.Args, "the tool ran", args)
			return false
		}
		assert.Equal(t, exitUsage, out.ExitCode, args)
		assert.Regexp(t, `^ERRO argument "-.*" is a flag; pass flags in "flags"\n$`, out.StdErr, args)
		require.NotNil(t, res)
		assert.True(t, res.IsError, args)
		assert.Nil(t, called.Args, "the tool did not run", args)
		return true
	}

	plain := refuseFlagArgs(nil)
	assert.False(t, refused(plain, "sind_get_node", "worker-0.dev"))
	assert.Equal(t, []string{"worker-0.dev"}, called.Args)
	for _, args := range [][]string{{"-v"}, {"worker-0", "--follow"}, {"--", "-x"}} {
		assert.True(t, refused(plain, "sind_logs", args...), args)
	}

	// exec's command after "--" may hold flags; its own arguments may not.
	assert.False(t, refused(plain, "sind_exec", "dev", "--", "ls", "-la"))
	assert.True(t, refused(plain, "sind_exec", "-v", "dev", "--", "ls"))
	assert.True(t, refused(plain, "sind_logs", "controller", "--", "-f"))

	// A -o in the arguments cannot override the forced JSON output.
	withJSON := refuseFlagArgs(forceJSONOutput)
	assert.True(t, refused(withJSON, "sind_get_nodes", "-o", "human"))
	assert.False(t, refused(withJSON, "sind_get_nodes", "dev"))
	assert.Equal(t, map[string]any{"output": "json"}, called.Flags)

	// An empty array reaches the tool as the flag given empty.
	flags := map[string]any{"device": []any{}, "cap-add": []any{"SYS_ADMIN"}, "count": float64(2)}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "sind_create_worker"}}
	_, _, err := plain(t.Context(), req, ophis.ToolInput{Flags: flags, Args: []string{"dev"}}, run)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"device": []any{""}, "cap-add": []any{"SYS_ADMIN"}, "count": float64(2)}, called.Flags)
}

func TestFailedAsError(t *testing.T) {
	call := func(res *mcp.CallToolResult, out ophis.ToolOutput, err error) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		run := func(context.Context, *mcp.CallToolRequest, ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
			return res, out, err
		}
		return failedAsError(run)(t.Context(), &mcp.CallToolRequest{}, ophis.ToolInput{})
	}

	res, _, err := call(nil, ophis.ToolOutput{StdOut: "ok"}, nil)
	require.NoError(t, err)
	assert.Nil(t, res, "a successful call is left alone")

	res, out, err := call(nil, ophis.ToolOutput{ExitCode: 1, StdErr: "boom"}, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.Equal(t, 1, out.ExitCode)

	kept := &mcp.CallToolResult{}
	res, _, err = call(kept, ophis.ToolOutput{ExitCode: 2}, nil)
	require.NoError(t, err)
	assert.Same(t, kept, res)
	assert.True(t, res.IsError)

	_, _, err = call(nil, ophis.ToolOutput{ExitCode: 1}, assert.AnError)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestEmptyListsAsNone(t *testing.T) {
	flags := map[string]any{"device": []any{}, "security-opt": []any{}, "cap-drop": []any{"MKNOD"}, "unmanaged": true}
	got := emptyListsAsNone(flags)
	assert.Equal(t, map[string]any{"device": []any{""}, "security-opt": []any{""}, "cap-drop": []any{"MKNOD"}, "unmanaged": true}, got)
	assert.Equal(t, []any{}, flags["device"], "the caller's flags are left alone")

	unchanged := map[string]any{"cap-add": []any{"SYS_ADMIN"}}
	assert.Equal(t, unchanged, emptyListsAsNone(unchanged))
	assert.Nil(t, emptyListsAsNone(nil))
}

// syncBuffer is a bytes.Buffer that a command goroutine writes while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startMCPStream runs `sind mcp stream` with args on a free port and
// returns its URL, its stderr, and the function that stops it and returns
// its error.
func startMCPStream(t *testing.T, args ...string) (string, *syncBuffer, func() error) {
	t.Helper()
	setVersion(t, "v1.2.3", "")
	// The server sets it for its tool calls, in this process.
	t.Setenv(envLogTimestamps, "")
	root := NewRootCommand()
	root.SetArgs(append([]string{"mcp", "stream", "--port", "0"}, args...))
	stderr := &syncBuffer{}
	root.SetOut(io.Discard)
	root.SetErr(stderr)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	stop := func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("the stream did not stop")
			return nil
		}
	}

	listening := regexp.MustCompile(`MCP server listening on (http://\S+)\n`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m := listening.FindStringSubmatch(stderr.String()); m != nil {
			return m[1], stderr, stop
		}
		select {
		case err := <-done:
			t.Fatalf("the stream ended: %v; stderr: %s", err, stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("the stream did not start; stderr: %s", stderr.String())
	return "", nil, nil
}

// bearerTransport adds a bearer token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

// postInitialize sends an MCP initialize request with the given
// Authorization header, if any, and returns the HTTP status.
func postInitialize(t *testing.T, url, authorization string) int {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestMCPStream_RequiresToken runs `sind mcp stream` and checks that it
// refuses a client without the generated bearer token, and serves sind's
// tools, through ophis, to one that sends it.
func TestMCPStream_RequiresToken(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(mcpTokenEnv, "")
	url, stderr, stop := startMCPStream(t)

	tokenPath := filepath.Join(stateHome, "sind", "mcp-token")
	data, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	token := strings.TrimSuffix(string(data), "\n")
	assert.Len(t, token, 26)
	info, err := os.Stat(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Contains(t, stderr.String(), "Bearer token in "+tokenPath)
	assert.Contains(t, stderr.String(), `"Authorization": "Bearer <token>"`)
	assert.NotContains(t, stderr.String(), token, "the token is not printed")

	assert.Equal(t, http.StatusUnauthorized, postInitialize(t, url, ""))
	assert.Equal(t, http.StatusUnauthorized, postInitialize(t, url, "Bearer wrong"))
	assert.Equal(t, http.StatusUnauthorized, postInitialize(t, url, "Basic "+token))

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             url,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	serverInfo := session.InitializeResult().ServerInfo
	assert.Equal(t, "sind", serverInfo.Name)
	assert.Equal(t, "1.2.3", serverInfo.Version)

	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	assert.Len(t, tools.Tools, 32)
	for _, tool := range tools.Tools {
		if tool.Name == "sind_create_worker" {
			require.NotNil(t, tool.Annotations.DestructiveHint)
			assert.True(t, *tool.Annotations.DestructiveHint)
		}
	}

	// The call reaches ophis's server, whose middleware refuses a flag in
	// the arguments before anything runs.
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "sind_get_nodes",
		Arguments: map[string]any{"flags": map[string]any{}, "args": []string{"-o", "human"}},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	var out ophis.ToolOutput
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, exitUsage, out.ExitCode)
	assert.Contains(t, out.StdErr, `argument "-o" is a flag`)

	require.NoError(t, session.Close())
	assert.NoError(t, stop(), "a stopped stream exits 0")
}

// TestRefuseFlagArgs_RunsToCompletion checks that the tools that change
// state keep running when their call is cancelled, and the others do not.
func TestRefuseFlagArgs_RunsToCompletion(t *testing.T) {
	var cancelable bool
	run := func(ctx context.Context, _ *mcp.CallToolRequest, _ ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		cancelable = ctx.Done() != nil
		return nil, ophis.ToolOutput{}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for tool, want := range map[string]bool{
		"sind_create_cluster": true,
		"sind_create_worker":  true,
		"sind_delete_cluster": true,
		"sind_delete_worker":  true,
		"sind_power_on":       true,
		"sind_power_cut":      true,
		"sind_exec":           false,
		"sind_logs":           false,
		"sind_get_ssh-config": false,
		"sind_doctor":         false,
		"sind_no_such_tool":   false,
	} {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool}}
		_, _, err := refuseFlagArgs(nil)(ctx, req, ophis.ToolInput{}, run)
		require.NoError(t, err, tool)
		assert.Equal(t, !want, cancelable, tool)
	}
}

func TestMCPStream_TokenFromEnv(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(mcpTokenEnv, "s3cret-token-0123456789")
	url, stderr, stop := startMCPStream(t)
	defer func() { assert.NoError(t, stop()) }()

	assert.Contains(t, stderr.String(), "Bearer token from "+mcpTokenEnv+"\n")
	assert.NoFileExists(t, filepath.Join(stateHome, "sind", "mcp-token"))
	assert.Equal(t, http.StatusUnauthorized, postInitialize(t, url, ""))
	assert.Equal(t, http.StatusOK, postInitialize(t, url, "Bearer s3cret-token-0123456789"))
}

func TestMCPStream_ListenError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(mcpTokenEnv, "s3cret-token-0123456789")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	_, _, err = executeCommand("mcp", "stream", "--port", port)
	require.ErrorContains(t, err, "listening")
	require.ErrorContains(t, err, "address already in use")
}

// TestMCPStream_ListenErrorKeepsToken checks that a stream that cannot
// have its port leaves the token file of the stream that has it alone.
func TestMCPStream_ListenErrorKeepsToken(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(mcpTokenEnv, "")
	tokenPath := filepath.Join(stateHome, "sind", mcpTokenFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0o700))
	require.NoError(t, os.WriteFile(tokenPath, []byte("running-stream-token\n"), 0o600))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	_, _, err = executeCommand("mcp", "stream", "--port", port)
	require.ErrorContains(t, err, "address already in use")
	got, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, "running-stream-token\n", string(got))
}

// TestMCPStream_TokenError checks that a token that cannot be made fails
// the stream after it has its port, and frees the port.
func TestMCPStream_TokenError(t *testing.T) {
	t.Setenv(mcpTokenEnv, "bad token with spaces")
	_, _, err := executeCommand("mcp", "stream", "--port", "0")
	require.ErrorContains(t, err, "a token is printable ASCII without spaces")
}

func TestMCPStreamToken(t *testing.T) {
	t.Run("invalid env", func(t *testing.T) {
		for _, token := range []string{"two words, long enough", "tab\there-and-more-chars", "nl-and-more-characters\n", "ümlaut-and-more-characters"} {
			t.Setenv(mcpTokenEnv, token)
			_, _, err := mcpStreamToken()
			require.EqualError(t, err, mcpTokenEnv+": a token is printable ASCII without spaces", token)
		}
	})
	t.Run("too short", func(t *testing.T) {
		t.Setenv(mcpTokenEnv, "a")
		_, _, err := mcpStreamToken()
		require.ErrorContains(t, err, "a token has at least 16 characters")
	})
	t.Run("new at every start", func(t *testing.T) {
		stateHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateHome)
		t.Setenv(mcpTokenEnv, "")
		path := filepath.Join(stateHome, "sind", "mcp-token")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

		token1, path1, err := mcpStreamToken()
		require.NoError(t, err)
		assert.Equal(t, path, path1)
		token2, _, err := mcpStreamToken()
		require.NoError(t, err)
		assert.NotEqual(t, token1, token2)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, token2+"\n", string(data))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "an older token file's mode is not kept")
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no temporary file is left")
	})
	t.Run("no state directory", func(t *testing.T) {
		t.Setenv(mcpTokenEnv, "")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		_, _, err := mcpStreamToken()
		require.ErrorContains(t, err, "resolving state directory")
	})
	t.Run("state directory cannot be created", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o600))
		t.Setenv(mcpTokenEnv, "")
		t.Setenv("XDG_STATE_HOME", file)
		_, _, err := mcpStreamToken()
		require.ErrorContains(t, err, "creating state directory")
	})
	t.Run("token file cannot be written", func(t *testing.T) {
		stateHome := t.TempDir()
		t.Setenv(mcpTokenEnv, "")
		t.Setenv("XDG_STATE_HOME", stateHome)
		// A directory where the token file goes makes the rename fail.
		require.NoError(t, os.MkdirAll(filepath.Join(stateHome, "sind", "mcp-token", "x"), 0o700))
		_, _, err := mcpStreamToken()
		require.ErrorContains(t, err, "writing MCP token")
		entries, err := os.ReadDir(filepath.Join(stateHome, "sind"))
		require.NoError(t, err)
		assert.Len(t, entries, 1, "the temporary file is removed")
	})
}

// TestAsMCPServer_Realm checks that a server command hands the root's
// --realm to the sind processes of its tool calls through SIND_REALM.
func TestAsMCPServer_Realm(t *testing.T) {
	t.Setenv("SIND_REALM", "env")
	// The server sets it for its tool calls, in this process.
	t.Setenv(envLogTimestamps, "")
	root := &cobra.Command{Use: "sind"}
	root.PersistentFlags().String("realm", "", "")
	var got string
	server := &cobra.Command{Use: "start", RunE: func(*cobra.Command, []string) error {
		got = os.Getenv("SIND_REALM")
		return nil
	}}
	root.AddCommand(server)
	asMCPServer(server)

	root.SetArgs([]string{"start"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "env", got, "without --realm, SIND_REALM stays")

	root.SetArgs([]string{"--realm", "ci", "start"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "ci", got)
}

// TestAsMCPServer_NoTimestamps checks that the error lines of a server's
// tool calls carry no time stamp.
func TestAsMCPServer_NoTimestamps(t *testing.T) {
	t.Setenv(envLogTimestamps, "")
	var got string
	server := &cobra.Command{Use: "start", RunE: func(*cobra.Command, []string) error {
		got = os.Getenv(envLogTimestamps)
		return nil
	}}
	asMCPServer(server)
	server.SetArgs(nil)
	require.NoError(t, server.Execute())
	assert.Equal(t, "false", got)
}

func TestStreamURL(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080/",
		"0.0.0.0:8080":   "http://localhost:8080/",
		"[::]:8080":      "http://localhost:8080/",
	} {
		tcp, err := net.ResolveTCPAddr("tcp", addr)
		require.NoError(t, err)
		assert.Equal(t, want, streamURL(tcp), addr)
	}
}

// TestMCPCommand_HelpStrings verifies that sind words the help of ophis's
// commands as its own: imperative Shorts and lowercase flag usages.
func TestMCPCommand_HelpStrings(t *testing.T) {
	stdout, _, err := executeCommand("--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "mcp         Serve sind's commands as MCP tools\n")

	for _, sub := range []string{"start", "stream", "tools"} {
		t.Run(sub, func(t *testing.T) {
			stdout, _, err := executeCommand("mcp", sub, "--help")
			require.NoError(t, err)
			assert.Contains(t, stdout, "--log-level string   log level of the MCP server (debug, info, warn, error)\n")
			assert.NotContains(t, stdout, "Log level")
		})
	}
}

func TestMCPToolArgs(t *testing.T) {
	in := ophis.ToolInput{
		Flags: map[string]any{
			"wait":    "5m",
			"count":   float64(2),
			"pull":    true,
			"dry-run": false,
			"image":   nil,
			"device":  []any{"/dev/a", "/dev/b"},
			"label":   map[string]any{"b": "2", "a": float64(1)},
		},
		Args: []string{"dev"},
	}
	assert.Equal(t, []string{
		"create", "worker",
		"--count", "2",
		"--device", "/dev/a", "--device", "/dev/b",
		"--label", "a=1", "--label", "b=2",
		"--pull",
		"--wait", "5m",
		"dev",
	}, mcpToolArgs("sind_create_worker", in))
	assert.Equal(t, []string{"get", "ssh-config"}, mcpToolArgs("sind_get_ssh-config", ophis.ToolInput{}))
}

func TestInProcessGroup(t *testing.T) {
	var got ophis.ExecuteFunc
	mw := inProcessGroup(func(_ context.Context, _ *mcp.CallToolRequest, _ ophis.ToolInput, run ophis.ExecuteFunc) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		got = run
		return nil, ophis.ToolOutput{}, nil
	})
	ophisRun := func(context.Context, *mcp.CallToolRequest, ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		t.Fatal("ophis's run function was called")
		return nil, ophis.ToolOutput{}, nil
	}
	_, _, err := mw(t.Context(), &mcp.CallToolRequest{}, ophis.ToolInput{}, ophisRun)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// mcpToolProcesses makes the sind processes mcpExecute starts the test
// binary acting as sind, with the fake docker on PATH, which hangs after
// it recorded its PID in the file it returns.
func mcpToolProcesses(t *testing.T) string {
	t.Helper()
	started := fakeDockerOnPath(t, "")
	t.Setenv(childEnv, "main")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return started
}

func TestMCPExecute_ExitCode(t *testing.T) {
	mcpToolProcesses(t)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "sind_version"}}
	_, out, err := mcpExecute(t.Context(), req, ophis.ToolInput{})
	require.NoError(t, err)
	assert.Equal(t, 0, out.ExitCode, out.StdErr)
	assert.Contains(t, out.StdOut, "sind")

	req.Params.Name = "sind_logs"
	_, out, err = mcpExecute(t.Context(), req, ophis.ToolInput{Args: []string{"Bad_Name"}})
	require.NoError(t, err)
	assert.Equal(t, exitUsage, out.ExitCode)
	assert.Contains(t, out.StdErr, "Bad_Name")
}

// TestMCPExecute_CancelKillsDocker checks that a cancelled call of a tool
// that runs docker ends at once, and takes the docker process with it:
// killed alone, the sind process left docker running, and docker held the
// call's output open until it ended.
func TestMCPExecute_CancelKillsDocker(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args []string
	}{
		{"sind_logs", []string{"controller"}},
		{"sind_exec", []string{"--", "sleep", "60"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			started := mcpToolProcesses(t)
			// The cluster has a submitter and a controller, so it is the
			// docker exec or docker logs that hangs, holding the call's
			// output.
			t.Setenv(fakeDockerPSEnv, `{"ID":"abc","Names":"sind-default-submitter","State":"running","Labels":"sind.realm=sind,sind.cluster=default,sind.role=submitter"}`+"\n"+
				`{"ID":"def","Names":"sind-default-controller","State":"running","Labels":"sind.realm=sind,sind.cluster=default,sind.role=controller"}`+"\n")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan ophis.ToolOutput, 1)
			go func() {
				req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tc.tool}}
				_, out, _ := mcpExecute(ctx, req, ophis.ToolInput{Args: tc.args})
				done <- out
			}()

			var pid int
			require.Eventually(t, func() bool {
				data, _ := os.ReadFile(started)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				return pid > 0
			}, 10*time.Second, 20*time.Millisecond, "docker was never started")
			cancel()

			select {
			case out := <-done:
				assert.NotEqual(t, 0, out.ExitCode)
			case <-time.After(5 * time.Second):
				t.Fatal("the cancelled call did not end")
			}
			assert.Eventually(t, func() bool { return !processRunning(pid) }, 5*time.Second, 20*time.Millisecond,
				"docker is still running")
		})
	}
}

// processRunning reports whether the process pid exists and is no zombie.
func processRunning(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	// The state follows the command name in parentheses, which may hold
	// spaces.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}
