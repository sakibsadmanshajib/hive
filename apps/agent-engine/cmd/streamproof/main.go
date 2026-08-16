//go:build proof

// Command streamproof is throwaway scaffolding that proves token deltas
// actually flow out of a live sandbox once a launched conversation carries
// llm.stream. It is not a product surface: the "proof" build tag keeps it out
// of every ordinary build, vet and test run, nothing imports it, and it adds
// no route to any daemon.
//
// # WHY A LIVE RUN IS THE ONLY EVIDENCE THAT COUNTS
//
// StreamingDeltaEvent is published straight to the agent-server's PubSub and
// deliberately never written to ConversationState.events (see the docstring on
// openhands/sdk/event/streaming_delta.py). So no post-hoc read of the event
// history can distinguish a conversation that streamed from one that did not:
// both replay the same final MessageEvent and no deltas at all. The evidence
// has to be collected while the conversation is still running, which is what
// this program does.
//
// # WHAT IT ACTUALLY EXERCISES
//
// It calls engineapi.New(...).Launch(...), the exact function the host
// launcher's POST /launch handler calls (apps/agent-engine/cmd/agent-engine/
// serve.go), against a real Apptainer sandbox built from the shipped SIF and a
// real model behind the Hive gateway. The single substitution is
// ResolveEgressHosts, which asks control-plane for a tenant's egress policy in
// the daemon and here returns the model host directly, exactly as serve.go
// appends it. That lookup cannot affect whether the agent-server publishes a
// token delta.
//
// # THE DIFFERENTIAL
//
// A capture that has never been observed to fail proves nothing, so this runs
// two conversations against the same sandbox, the same model and the same
// prompt, differing only in the flag:
//
//	A: created by engine.Launch, so it carries whatever the product sends today.
//	B: created here through the same controlclient with stream explicitly false.
//
// A must produce delta frames and B must produce none. Either half failing
// fails the program, because "no deltas anywhere" and "deltas everywhere" are
// both evidence that the capture is not measuring the flag.
//
// The WebSocket client below is hand written and deliberately minimal (read
// frames, answer pings, no extensions, no compression). A throwaway proof is
// not a reason to add a dependency to a module whose whole requirement list is
// two lines.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
)

const (
	// Small on purpose: this run is billed against a real Hive account, and a
	// one sentence answer still produces several token deltas.
	proofPrompt = "Reply with exactly this sentence and nothing else: Hive streaming proof ok."

	deltaKind           = "StreamingDeltaEvent"
	conversationTimeout = 5 * time.Minute
	socketWaitTimeout   = 4 * time.Minute
)

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Fatalf("streamproof: %v", err)
	}
}

type recorder struct {
	out *os.File
}

func (r *recorder) printf(format string, args ...any) {
	line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(format, args...))
	fmt.Println(line)
	if r.out != nil {
		fmt.Fprintln(r.out, line)
	}
}

func run() error {
	rec := &recorder{}
	if path := os.Getenv("HIVE_PROOF_LOG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create proof log %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		rec.out = f
	}

	cfg, sessionKey, err := configFromEnv()
	if err != nil {
		return err
	}
	rec.printf("config sif=%s packs=%s run_dir=%s model=%s base_url=%s",
		cfg.SIFPath, cfg.PacksDir, cfg.RunDir, cfg.LLMModel, cfg.LLMBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*conversationTimeout+2*time.Minute)
	defer cancel()

	eng := engineapi.New(cfg)
	task := engineapi.Task{
		ID:           uuid.New(),
		TenantID:     uuid.New(),
		UserID:       uuid.New(),
		Pack:         envOr("HIVE_PROOF_PACK", "coding-pack"),
		Instructions: proofPrompt,
	}
	rec.printf("launching sandbox task=%s pack=%s", task.ID, task.Pack)
	launchedAt := time.Now().UTC()
	sessionRef, err := eng.Launch(ctx, task)
	if err != nil {
		return fmt.Errorf("launch: %w", err)
	}
	rec.printf("launch returned session_ref=%s after %s", sessionRef, time.Since(launchedAt).Round(time.Millisecond))
	defer func() {
		// Cancel rather than leak: the sandbox process, its directories and
		// its quota slot are all freed here even when the proof fails.
		if err := eng.Cancel(context.Background(), sessionRef); err != nil {
			rec.printf("cleanup cancel: %v", err)
		}
	}()

	socketPath, err := findControlSocket(cfg.RunDir)
	if err != nil {
		return err
	}
	rec.printf("control socket %s", socketPath)

	client := controlclient.New(socketPath, sessionKey)

	convoID, err := uuid.Parse(sessionRef)
	if err != nil {
		return fmt.Errorf("session ref %q is not a conversation id: %w", sessionRef, err)
	}
	streamed, err := observe(ctx, rec, socketPath, sessionKey, client, convoID, "A/product(stream=true)")
	if err != nil {
		return err
	}

	control, err := observeControl(ctx, rec, socketPath, sessionKey, client, cfg)
	if err != nil {
		return err
	}

	rec.printf("SUMMARY A/product(stream=true): %s", streamed)
	rec.printf("SUMMARY B/control(stream=false): %s", control)

	var problems []string
	if streamed.deltas() == 0 {
		problems = append(problems, "the product launch published no StreamingDeltaEvent")
	}
	if n := control.deltas(); n != 0 {
		problems = append(problems, fmt.Sprintf("the stream=false control published %d StreamingDeltaEvent frames, so this capture is not measuring the flag", n))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	rec.printf("PROOF OK: %d delta frames on the product launch, 0 on the stream=false control", streamed.deltas())
	return nil
}

func configFromEnv() (engineapi.Config, string, error) {
	var missing []string
	get := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	sif := get("HIVE_AGENT_ENGINE_SIF_PATH")
	packs := get("HIVE_AGENT_ENGINE_PACKS_DIR")
	workspace := get("HIVE_AGENT_ENGINE_WORKSPACE_ROOT")
	runDir := get("HIVE_AGENT_ENGINE_RUN_DIR")
	model := get("HIVE_AGENT_ENGINE_LLM_MODEL")
	baseURL := get("HIVE_AGENT_ENGINE_LLM_BASE_URL")
	apiKey := get("HIVE_AGENT_ENGINE_LLM_API_KEY")
	if len(missing) > 0 {
		return engineapi.Config{}, "", fmt.Errorf("missing %v", missing)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return engineapi.Config{}, "", fmt.Errorf("HIVE_AGENT_ENGINE_LLM_BASE_URL is not a URL with a host: %q", baseURL)
	}
	host := parsed.Hostname()
	for _, dir := range []string{workspace, runDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return engineapi.Config{}, "", fmt.Errorf("create %s: %w", dir, err)
		}
	}
	cfg := engineapi.Config{
		SIFPath:       sif,
		PacksDir:      packs,
		WorkspaceRoot: workspace,
		RunDir:        runDir,
		// serve.go resolves the tenant's egress policy from control-plane and
		// appends the model host to it. There is no control-plane here, and
		// the policy has no bearing on token streaming, so the model host is
		// the whole allowlist.
		ResolveEgressHosts: func(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
			return []string{host}, nil
		},
		LLMModel:      model,
		LLMBaseURL:    baseURL,
		LLMAPIKey:     apiKey,
		SessionAPIKey: os.Getenv("HIVE_AGENT_ENGINE_SESSION_API_KEY"),
		MemoryLimit:   envOr("HIVE_SANDBOX_MEMORY_LIMIT", "2G"),
		CPULimit:      envOr("HIVE_SANDBOX_CPU_LIMIT", "2"),
	}
	return cfg, cfg.SessionAPIKey, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// findControlSocket locates the per-session control socket Launch created.
// engine keeps the path private (it is derived from an os.MkdirTemp name), and
// exporting it for a proof would be product surface added for scaffolding.
func findControlSocket(runDir string) (string, error) {
	deadline := time.Now().Add(socketWaitTimeout)
	for {
		matches, err := filepath.Glob(filepath.Join(runDir, "*", "c", "agent.sock"))
		if err != nil {
			return "", fmt.Errorf("glob control sockets: %w", err)
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			sort.Strings(matches)
			return "", fmt.Errorf("%d control sockets under %s, refusing to guess which session is under proof: %v", len(matches), runDir, matches)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no control socket appeared under %s", runDir)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// observeControl runs the differential arm: the same prompt against the same
// sandbox and model, launched through the same client, with stream off.
func observeControl(ctx context.Context, rec *recorder, socketPath, sessionKey string, client *controlclient.Client, cfg engineapi.Config) (*capture, error) {
	req := controlclient.StartConversationRequest{
		Workspace: controlclient.LocalWorkspace("/workspace"),
		AgentSettings: &controlclient.AgentSettings{
			AgentKind: "openhands",
			LLM: controlclient.LLMSettings{
				Model:   cfg.LLMModel,
				BaseURL: cfg.LLMBaseURL,
				APIKey:  cfg.LLMAPIKey,
				UsageID: "hive-agent-stream-control",
				Stream:  false,
			},
		},
		InitialMessage: &controlclient.SendMessageRequest{
			Role:    "user",
			Content: []controlclient.TextContent{controlclient.Text(proofPrompt)},
			Run:     false,
		},
	}
	convo, err := client.StartConversation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("control start conversation: %w", err)
	}
	rec.printf("control conversation %s created with stream=false", convo.ID)
	if err := client.Run(ctx, convo.ID); err != nil {
		return nil, fmt.Errorf("control run conversation: %w", err)
	}
	return observe(ctx, rec, socketPath, sessionKey, client, convo.ID, "B/control(stream=false)")
}

// observe subscribes to a conversation's event socket and polls its execution
// status until it leaves the running states, so every frame it counts is
// timestamped against a status that was still live.
func observe(ctx context.Context, rec *recorder, socketPath, sessionKey string, client *controlclient.Client, convoID uuid.UUID, label string) (*capture, error) {
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()

	capt := &capture{label: label, counts: map[string]int{}}
	conn, err := dialEvents(watchCtx, socketPath, sessionKey, convoID)
	if err != nil {
		return nil, fmt.Errorf("%s: subscribe to events: %w", label, err)
	}
	defer func() { _ = conn.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		capt.collect(conn, rec)
	}()

	status, err := awaitTerminal(watchCtx, rec, client, convoID, label)
	if err != nil {
		return nil, err
	}
	capt.terminalStatus = status
	capt.terminalAt = time.Now().UTC()
	// The agent-server flushes its last deltas just before it records the
	// final message, so a socket closed the instant a poll reports terminal
	// can drop frames that were already on the wire.
	time.Sleep(2 * time.Second)
	stop()
	_ = conn.SetReadDeadline(time.Now())
	wg.Wait()
	return capt, nil
}

type capture struct {
	label          string
	mu             sync.Mutex
	counts         map[string]int
	firstDelta     time.Time
	lastDelta      time.Time
	terminalAt     time.Time
	terminalStatus controlclient.ExecutionStatus
	samples        int
}

func (c *capture) deltas() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[deltaKind]
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	kinds := make([]string, 0, len(c.counts))
	for kind := range c.counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, c.counts[kind]))
	}
	window := "no deltas"
	if !c.firstDelta.IsZero() {
		window = fmt.Sprintf("first delta %s, last delta %s, terminal (%s) observed %s",
			c.firstDelta.Format(time.RFC3339Nano), c.lastDelta.Format(time.RFC3339Nano),
			c.terminalStatus, c.terminalAt.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf("frames{%s}; %s", strings.Join(parts, " "), window)
}

func (c *capture) collect(conn *wsConn, rec *recorder) {
	for {
		payload, err := conn.readMessage()
		if err != nil {
			return
		}
		var envelope struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Kind == "" {
			continue
		}
		now := time.Now().UTC()
		c.mu.Lock()
		c.counts[envelope.Kind]++
		if envelope.Kind == deltaKind {
			if c.firstDelta.IsZero() {
				c.firstDelta = now
			}
			c.lastDelta = now
			if c.samples < 3 {
				c.samples++
				rec.printf("%s DELTA #%d %s", c.label, c.counts[deltaKind], truncate(string(payload), 400))
			}
		}
		c.mu.Unlock()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func awaitTerminal(ctx context.Context, rec *recorder, client *controlclient.Client, convoID uuid.UUID, label string) (controlclient.ExecutionStatus, error) {
	deadline := time.Now().Add(conversationTimeout)
	var last controlclient.ExecutionStatus
	for {
		info, err := client.GetConversation(ctx, convoID)
		if err != nil {
			return "", fmt.Errorf("%s: get conversation: %w", label, err)
		}
		if info.ExecutionStatus != last {
			rec.printf("%s execution_status=%s", label, info.ExecutionStatus)
			last = info.ExecutionStatus
		}
		switch info.ExecutionStatus {
		case controlclient.StatusFinished, controlclient.StatusErrored, controlclient.StatusStuck,
			controlclient.StatusPaused, controlclient.StatusDeleting:
			return info.ExecutionStatus, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s: still %s after %s", label, info.ExecutionStatus, conversationTimeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// --- minimal RFC 6455 client: reads frames, answers pings, sends nothing else ---

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
}

func (c *wsConn) Close() error                      { return c.conn.Close() }
func (c *wsConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

func dialEvents(ctx context.Context, socketPath, sessionKey string, convoID uuid.UUID) (*wsConn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		_ = conn.Close()
		return nil, err
	}
	path := "/sockets/events/" + convoID.String()
	if sessionKey != "" {
		// Query parameter auth is deprecated upstream but still accepted, and
		// it keeps this client to reads plus pongs. The socket is a Unix
		// socket on the launcher host, so the key never crosses a network.
		path += "?session_api_key=" + url.QueryEscape(sessionKey)
	}
	handshake := "GET " + path + " HTTP/1.1\r\n" +
		"Host: agent-server.control\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(nonce) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, handshake); err != nil {
		_ = conn.Close()
		return nil, err
	}
	r := bufio.NewReader(conn)
	statusLine, err := r.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, "101") {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake refused: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return &wsConn{conn: conn, r: r}, nil
}

// readMessage returns the next complete text or binary message, answering
// pings and stopping on close.
func (c *wsConn) readMessage() ([]byte, error) {
	var message []byte
	for {
		final, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x0:
			message = append(message, payload...)
		case 0x1, 0x2:
			message = append(message[:0], payload...)
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		default:
			return nil, fmt.Errorf("unexpected websocket opcode %d", opcode)
		}
		if final {
			return message, nil
		}
	}
}

func (c *wsConn) readFrame() (final bool, opcode byte, payload []byte, err error) {
	header := make([]byte, 2)
	if _, err = io.ReadFull(c.r, header); err != nil {
		return false, 0, nil, err
	}
	final = header[0]&0x80 != 0
	opcode = header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.r, ext); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.r, ext); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext)
	}
	if length > 8<<20 {
		return false, 0, nil, fmt.Errorf("websocket frame of %d bytes is larger than this proof client accepts", length)
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.r, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return final, opcode, payload, nil
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) < 1<<16:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[2:], uint16(len(payload)))
	default:
		return fmt.Errorf("payload of %d bytes is larger than this proof client sends", len(payload))
	}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := c.conn.Write(append(header, masked...))
	return err
}
