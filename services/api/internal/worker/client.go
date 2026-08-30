package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Client struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	sequence atomic.Uint64
	pending  map[uint64]chan rpcResponse
	closed   chan struct{}
	state    atomic.Value
}

func Start(ctx context.Context, command string, args []string, workingDirectory, dataRoot string) (*Client, error) {
	if command == "" {
		return nil, errors.New("worker command is not configured")
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workingDirectory
	cmd.Env = append(cmd.Environ(), "KAH_DATA_ROOT="+dataRoot, "PYTHONIOENCODING=utf-8")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	client := &Client{cmd: cmd, stdin: stdin, pending: map[uint64]chan rpcResponse{}, closed: make(chan struct{})}
	client.state.Store("starting")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go client.read(stdout)
	go client.drainErrors(stderr)
	go client.wait()
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result map[string]any
	if err := client.Call(healthCtx, "health", map[string]any{}, &result); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("worker health failed: %w", err)
	}
	client.state.Store("ok")
	return client, nil
}

func (c *Client) State() string {
	if c == nil {
		return "unavailable"
	}
	if value := c.state.Load(); value != nil {
		return value.(string)
	}
	return "unknown"
}

func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if c == nil {
		return errors.New("worker unavailable")
	}
	id := c.sequence.Add(1)
	response := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = response
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err == nil {
		_, err = c.stdin.Write(append(payload, '\n'))
	}
	c.mu.Unlock()
	if err != nil {
		c.remove(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.remove(id)
		return ctx.Err()
	case <-c.closed:
		return errors.New("worker stopped")
	case value, ok := <-response:
		if !ok {
			return errors.New("worker stopped")
		}
		if value.Error != nil {
			return fmt.Errorf("worker error %d: %s", value.Error.Code, value.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(value.Result, out)
	}
}

func (c *Client) remove(id uint64) { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }
func (c *Client) read(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var response rpcResponse
		if json.Unmarshal(scanner.Bytes(), &response) != nil {
			continue
		}
		c.mu.Lock()
		channel := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if channel != nil {
			channel <- response
		}
	}
}
func (c *Client) drainErrors(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
	}
}
func (c *Client) wait() {
	_ = c.cmd.Wait()
	c.state.Store("stopped")
	close(c.closed)
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
}
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	_ = c.stdin.Close()
	var killErr error
	if c.cmd.Process != nil {
		killErr = c.cmd.Process.Kill()
	}
	// Process.Kill returns before Wait has reaped the child. On Windows the
	// worker may still hold files under KAH_DATA_ROOT during that interval,
	// which makes Store.Close and test/temp-directory cleanup nondeterministic.
	select {
	case <-c.closed:
	case <-time.After(5 * time.Second):
		if killErr == nil {
			return errors.New("timed out waiting for worker to stop")
		}
	}
	return killErr
}
