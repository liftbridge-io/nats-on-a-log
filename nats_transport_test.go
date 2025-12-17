// Copyright 2017 Apcera Inc. All rights reserved.

package natslog

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/nats-io/nats-server/v2/server"
	gnatsd "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

type testAddrProvider struct {
	addr string
}

func (t *testAddrProvider) ServerAddr(id raft.ServerID) (raft.ServerAddress, error) {
	return raft.ServerAddress(t.addr), nil
}

// This can be used as the destination for a logger and it'll
// map them into calls to testing.T.Log, so that you only see
// the logging for failed tests.
type testLoggerAdapter struct {
	t      *testing.T
	prefix string
}

func (a *testLoggerAdapter) Write(d []byte) (int, error) {
	if d[len(d)-1] == '\n' {
		d = d[:len(d)-1]
	}
	if a.prefix != "" {
		l := a.prefix + ": " + string(d)
		a.t.Log(l)
		return len(l), nil
	}

	a.t.Log(string(d))
	return len(d), nil
}

func newTestLogger(t *testing.T) hclog.Logger {
	options := &hclog.LoggerOptions{
		Output: &testLoggerAdapter{t: t},
	}
	return hclog.New(options)
}

// So that we can pass tests and benchmarks...
type tLogger interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// RunDefaultServer will run a server on the default port.
func RunDefaultServer() *server.Server {
	return RunServerOnPort(nats.DefaultPort)
}

// RunServerOnPort will run a server on the given port.
func RunServerOnPort(port int) *server.Server {
	opts := gnatsd.DefaultTestOptions
	opts.Port = port
	return RunServerWithOptions(opts)
}

// RunServerWithOptions will run a server with the given options.
func RunServerWithOptions(opts server.Options) *server.Server {
	return gnatsd.RunServer(&opts)
}

func NewDefaultConnection(t tLogger) *nats.Conn {
	return NewConnection(t, nats.DefaultPort)
}

// NewConnection forms connection on a given port.
func NewConnection(t tLogger, port int) *nats.Conn {
	url := fmt.Sprintf("nats://localhost:%d", port)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("Failed to create default connection: %v\n", err)
		return nil
	}
	return nc
}

func TestNATSTransportStartStop(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	trans, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	trans.Close()
}

func TestNATSTransportHeartbeatFastPath(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()

	// Make the RPC request
	args := raft.AppendEntriesRequest{
		Term:   10,
		Leader: []byte("cartman"),
	}
	resp := raft.AppendEntriesResponse{
		Term:    4,
		LastLog: 90,
		Success: true,
	}

	invoked := false
	fastpath := func(rpc raft.RPC) {
		// Verify the command
		req := rpc.Command.(*raft.AppendEntriesRequest)
		if !reflect.DeepEqual(req, &args) {
			t.Fatalf("command mismatch: %#v %#v", *req, args)
		}

		rpc.Respond(&resp, nil)
		invoked = true
	}
	trans1.SetHeartbeatHandler(fastpath)

	// Transport 2 makes outbound request
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	var out raft.AppendEntriesResponse
	if err := trans2.AppendEntries("id1", trans1.LocalAddr(), &args, &out); err != nil {
		t.Fatalf("err: %v", err)
	}

	// Verify the response
	if !reflect.DeepEqual(resp, out) {
		t.Fatalf("command mismatch: %#v %#v", resp, out)
	}

	// Ensure fast-path is used
	if !invoked {
		t.Fatalf("fast-path not used")
	}
}

func TestNATSTransportAppendEntries(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()
	rpcCh := trans1.Consumer()

	// Make the RPC request
	args := raft.AppendEntriesRequest{
		Term:         10,
		Leader:       []byte("cartman"),
		PrevLogEntry: 100,
		PrevLogTerm:  4,
		Entries: []*raft.Log{
			{
				Index: 101,
				Term:  4,
				Type:  raft.LogNoop,
			},
		},
		LeaderCommitIndex: 90,
	}
	resp := raft.AppendEntriesResponse{
		Term:    4,
		LastLog: 90,
		Success: true,
	}

	ch := make(chan error, 1)

	// Listen for a request
	go func() {
		select {
		case rpc := <-rpcCh:
			// Verify the command
			req := rpc.Command.(*raft.AppendEntriesRequest)
			if !reflect.DeepEqual(req, &args) {
				ch <- fmt.Errorf("command mismatch: %#v %#v", *req, args)
				return
			}

			rpc.Respond(&resp, nil)

		case <-time.After(200 * time.Millisecond):
			ch <- errors.New("timeout")
			return
		}
		close(ch)
	}()

	// Transport 2 makes outbound request
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	var out raft.AppendEntriesResponse
	if err := trans2.AppendEntries("id1", trans1.LocalAddr(), &args, &out); err != nil {
		t.Fatalf("err: %v", err)
	}

	err = <-ch
	if err != nil {
		t.Fatal(err)
	}

	// Verify the response
	if !reflect.DeepEqual(resp, out) {
		t.Fatalf("command mismatch: %#v %#v", resp, out)
	}
}

func TestNATSTransportAppendEntriesPipeline(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()
	rpcCh := trans1.Consumer()

	// Make the RPC request
	args := raft.AppendEntriesRequest{
		Term:         10,
		Leader:       []byte("cartman"),
		PrevLogEntry: 100,
		PrevLogTerm:  4,
		Entries: []*raft.Log{
			{
				Index: 101,
				Term:  4,
				Type:  raft.LogNoop,
			},
		},
		LeaderCommitIndex: 90,
	}
	resp := raft.AppendEntriesResponse{
		Term:    4,
		LastLog: 90,
		Success: true,
	}

	// Transport 2 makes outbound request
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	pipeline, err := trans2.AppendEntriesPipeline("id1", trans1.LocalAddr())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer pipeline.Close()

	// Start listening for requests in a goroutine.
	// With raft v1.7.3, the default maxInFlight is 2, so the pipeline has very
	// limited buffering. We need to consume responses concurrently to avoid
	// blocking the pipeline.
	listenerDone := make(chan error, 1)
	go func() {
		for i := 0; i < 10; i++ {
			select {
			case rpc := <-rpcCh:
				// Verify the command
				req := rpc.Command.(*raft.AppendEntriesRequest)
				if !reflect.DeepEqual(req, &args) {
					listenerDone <- fmt.Errorf("command mismatch: %#v %#v", *req, args)
					return
				}
				rpc.Respond(&resp, nil)

			case <-time.After(5 * time.Second):
				listenerDone <- errors.New("listener timeout")
				return
			}
		}
		close(listenerDone)
	}()

	// Start consuming responses concurrently to prevent pipeline backpressure
	respCh := pipeline.Consumer()
	consumerDone := make(chan error, 1)
	go func() {
		for i := 0; i < 10; i++ {
			select {
			case ready := <-respCh:
				// Verify the response
				if !reflect.DeepEqual(&resp, ready.Response()) {
					consumerDone <- fmt.Errorf("response mismatch: %#v %#v", &resp, ready.Response())
					return
				}
			case <-time.After(5 * time.Second):
				consumerDone <- fmt.Errorf("consumer timeout waiting for response %d", i)
				return
			}
		}
		close(consumerDone)
	}()

	// Send all requests - this won't block now because responses are being consumed
	for i := 0; i < 10; i++ {
		out := new(raft.AppendEntriesResponse)
		if _, err := pipeline.AppendEntries(&args, out); err != nil {
			t.Fatalf("err: %v", err)
		}
	}

	// Wait for both goroutines to complete
	if err := <-listenerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-consumerDone; err != nil {
		t.Fatal(err)
	}
}

func TestNATSTransportRequestVote(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()
	rpcCh := trans1.Consumer()

	// Make the RPC request
	args := raft.RequestVoteRequest{
		Term:         20,
		Candidate:    []byte("butters"),
		LastLogIndex: 100,
		LastLogTerm:  19,
	}
	resp := raft.RequestVoteResponse{
		Term:    100,
		Peers:   []byte("blah"),
		Granted: false,
	}

	ch := make(chan error, 1)

	// Listen for a request
	go func() {
		select {
		case rpc := <-rpcCh:
			// Verify the command
			req := rpc.Command.(*raft.RequestVoteRequest)
			if !reflect.DeepEqual(req, &args) {
				ch <- fmt.Errorf("command mismatch: %#v %#v", *req, args)
				return
			}

			rpc.Respond(&resp, nil)

		case <-time.After(200 * time.Millisecond):
			ch <- errors.New("timeout")
			return
		}
		close(ch)
	}()

	// Transport 2 makes outbound request
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	var out raft.RequestVoteResponse
	if err := trans2.RequestVote("id1", trans1.LocalAddr(), &args, &out); err != nil {
		t.Fatalf("err: %v", err)
	}

	err = <-ch
	if err != nil {
		t.Fatal(err)
	}

	// Verify the response
	if !reflect.DeepEqual(resp, out) {
		t.Fatalf("command mismatch: %#v %#v", resp, out)
	}
}

func TestNATSTransportInstallSnapshot(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()
	rpcCh := trans1.Consumer()

	// Make the RPC request
	args := raft.InstallSnapshotRequest{
		Term:         10,
		Leader:       []byte("kyle"),
		LastLogIndex: 100,
		LastLogTerm:  9,
		Peers:        []byte("blah blah"),
		Size:         10,
	}
	resp := raft.InstallSnapshotResponse{
		Term:    10,
		Success: true,
	}

	ch := make(chan error, 1)

	// Listen for a request
	go func() {
		select {
		case rpc := <-rpcCh:
			// Verify the command
			req := rpc.Command.(*raft.InstallSnapshotRequest)
			if !reflect.DeepEqual(req, &args) {
				ch <- fmt.Errorf("command mismatch: %#v %#v", *req, args)
				return
			}

			// Try to read the bytes
			buf := make([]byte, 10)
			rpc.Reader.Read(buf)

			// Compare
			if !bytes.Equal(buf, []byte("0123456789")) {
				ch <- fmt.Errorf("bad buf %v", buf)
				return
			}

			rpc.Respond(&resp, nil)

		case <-time.After(200 * time.Millisecond):
			ch <- errors.New("timeout")
			return
		}
		close(ch)
	}()

	// Transport 2 makes outbound request
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	// Create a buffer
	buf := bytes.NewBuffer([]byte("0123456789"))

	var out raft.InstallSnapshotResponse
	if err := trans2.InstallSnapshot("id1", trans1.LocalAddr(), &args, &out, buf); err != nil {
		t.Fatalf("err: %v", err)
	}

	err = <-ch
	if err != nil {
		t.Fatal(err)
	}

	// Verify the response
	if !reflect.DeepEqual(resp, out) {
		t.Fatalf("command mismatch: %#v %#v", resp, out)
	}
}

func TestNATSTransportEncodeDecode(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()

	local := trans1.LocalAddr()
	enc := trans1.EncodePeer("id1", local)
	dec := trans1.DecodePeer(enc)

	if dec != local {
		t.Fatalf("enc/dec fail: %v %v", dec, local)
	}
}

func TestNATSTransportEncodeDecodeAddressProvider(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	addressOverride := "b"
	config := &raft.NetworkTransportConfig{MaxPool: 2, Timeout: time.Second, Logger: newTestLogger(t), ServerAddressProvider: &testAddrProvider{addressOverride}}
	trans1, err := NewNATSTransportWithConfig("a", "", nc, config)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()

	local := trans1.LocalAddr()
	enc := trans1.EncodePeer("id1", local)
	dec := trans1.DecodePeer(enc)

	if dec != raft.ServerAddress(addressOverride) {
		t.Fatalf("enc/dec fail: %v %v", dec, addressOverride)
	}
}

func TestNATSTransportPooledConn(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Transport 1 is consumer
	trans1, err := NewNATSTransportWithLogger("a", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans1.Close()
	rpcCh := trans1.Consumer()

	// Make the RPC request
	args := raft.AppendEntriesRequest{
		Term:         10,
		Leader:       []byte("cartman"),
		PrevLogEntry: 100,
		PrevLogTerm:  4,
		Entries: []*raft.Log{
			{
				Index: 101,
				Term:  4,
				Type:  raft.LogNoop,
			},
		},
		LeaderCommitIndex: 90,
	}
	resp := raft.AppendEntriesResponse{
		Term:    4,
		LastLog: 90,
		Success: true,
	}

	ch1 := make(chan error, 1)

	// Listen for a request
	go func() {
		for {
			select {
			case rpc := <-rpcCh:
				// Verify the command
				req := rpc.Command.(*raft.AppendEntriesRequest)
				if !reflect.DeepEqual(req, &args) {
					ch1 <- fmt.Errorf("command mismatch: %#v %#v", *req, args)
					return
				}
				rpc.Respond(&resp, nil)

			case <-time.After(200 * time.Millisecond):
				close(ch1)
				return
			}
		}
	}()

	// Transport 2 makes outbound request, 3 conn pool
	trans2, err := NewNATSTransportWithLogger("b", "", nc, time.Second, newTestLogger(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()

	// Create wait group
	wg := &sync.WaitGroup{}
	wg.Add(5)

	ch2 := make(chan error, 1)

	appendFunc := func() {
		defer wg.Done()
		var out raft.AppendEntriesResponse
		if err := trans2.AppendEntries("id1", trans1.LocalAddr(), &args, &out); err != nil {
			ch2 <- fmt.Errorf("err: %v", err)
			return
		}

		// Verify the response
		if !reflect.DeepEqual(resp, out) {
			ch2 <- fmt.Errorf("command mismatch: %#v %#v", resp, out)
			return
		}
	}

	// Try to do parallel appends, should stress the conn pool
	for i := 0; i < 5; i++ {
		go appendFunc()
	}

	// Wait for the routines to finish
	wg.Wait()
	close(ch2)

	err = <-ch1
	if err != nil {
		t.Fatal(err)
	}

	err = <-ch2
	if err != nil {
		t.Fatal(err)
	}
}

// TestNatsAddrNetwork tests the Network() method of natsAddr.
func TestNatsAddrNetwork(t *testing.T) {
	addr := natsAddr("test-node")
	if addr.Network() != "nats" {
		t.Fatalf("expected network 'nats', got '%s'", addr.Network())
	}
	if addr.String() != "test-node" {
		t.Fatalf("expected string 'test-node', got '%s'", addr.String())
	}
}

// TestNewNATSTransport tests NewNATSTransport with default logger.
func TestNewNATSTransport(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Test with nil logOutput (uses os.Stderr)
	trans, err := NewNATSTransport("a", "", nc, time.Second, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans.Close()

	// Test with custom logOutput
	trans2, err := NewNATSTransport("b", "", nc, time.Second, &testLoggerAdapter{t: t})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans2.Close()
}

// TestNewNATSTransportWithConfigZeroTimeout tests NewNATSTransportWithConfig with zero timeout.
func TestNewNATSTransportWithConfigZeroTimeout(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	config := &raft.NetworkTransportConfig{
		MaxPool: 2,
		Timeout: 0, // Zero timeout should default to 2 seconds
		Logger:  newTestLogger(t),
	}
	trans, err := NewNATSTransportWithConfig("a", "", nc, config)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer trans.Close()
}

// TestNatsConnClosedReadWrite tests read/write operations on closed connection.
func TestNatsConnClosedReadWrite(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	// Create a stream layer directly to test natsConn
	stream, err := newNATSStreamLayer("a", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream.Close()

	stream2, err := newNATSStreamLayer("b", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream2.Close()

	// Start accepting on stream
	acceptDone := make(chan struct{})
	var acceptedConn *natsConn
	go func() {
		conn, err := stream.Accept()
		if err != nil {
			return
		}
		acceptedConn = conn.(*natsConn)
		close(acceptDone)
	}()

	// Dial from stream2 to stream
	conn, err := stream2.Dial(raft.ServerAddress(stream.Addr().String()), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Wait for accept
	select {
	case <-acceptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for accept")
	}

	// Get the underlying natsConn
	nConn := conn.(*natsConn)

	// Test LocalAddr
	localAddr := nConn.LocalAddr()
	if localAddr == nil {
		t.Fatal("LocalAddr should not be nil")
	}

	// Test writing empty bytes (should return immediately)
	n, err := nConn.Write([]byte{})
	if err != nil {
		t.Fatalf("err writing empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written for empty write, got %d", n)
	}

	// Close the connection
	if err := nConn.Close(); err != nil {
		t.Fatalf("err closing: %v", err)
	}

	// Test reading from closed connection
	buf := make([]byte, 10)
	_, err = nConn.Read(buf)
	if err == nil {
		t.Fatal("expected error reading from closed conn")
	}

	// Test writing to closed connection
	_, err = nConn.Write([]byte("test"))
	if err == nil {
		t.Fatal("expected error writing to closed conn")
	}

	// Clean up accepted connection
	if acceptedConn != nil {
		acceptedConn.Close()
	}
}

// TestNatsConnSetDeadline tests SetDeadline method.
func TestNatsConnSetDeadline(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	stream, err := newNATSStreamLayer("a", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream.Close()

	stream2, err := newNATSStreamLayer("b", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream2.Close()

	// Start accepting on stream
	go func() {
		conn, err := stream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}()

	// Dial from stream2 to stream
	conn, err := stream2.Dial(raft.ServerAddress(stream.Addr().String()), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer conn.Close()

	nConn := conn.(*natsConn)

	// Test SetDeadline
	deadline := time.Now().Add(time.Second)
	if err := nConn.SetDeadline(deadline); err != nil {
		t.Fatalf("err setting deadline: %v", err)
	}
}

// TestStreamLayerDialNotConnected tests Dial when NATS is not connected.
func TestStreamLayerDialNotConnected(t *testing.T) {
	s := RunDefaultServer()
	nc := NewDefaultConnection(t)

	stream, err := newNATSStreamLayer("a", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream.Close()

	// Close the NATS connection to simulate disconnected state
	nc.Close()
	s.Shutdown()

	// Try to dial - should fail because not connected
	_, err = stream.Dial("b", time.Second)
	if err == nil {
		t.Fatal("expected error dialing with closed connection")
	}
}

// TestStreamLayerDialTimeout tests Dial timeout when peer doesn't respond.
func TestStreamLayerDialTimeout(t *testing.T) {
	s := RunDefaultServer()
	defer s.Shutdown()
	nc := NewDefaultConnection(t)
	defer nc.Close()

	stream, err := newNATSStreamLayer("a", "", nc, newTestLogger(t), time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer stream.Close()

	// Try to dial a non-existent peer - should timeout
	_, err = stream.Dial("nonexistent", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error dialing non-existent peer")
	}
}
