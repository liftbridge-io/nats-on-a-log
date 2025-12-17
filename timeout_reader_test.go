// Copyright 2017 Apcera Inc. All rights reserved.

package natslog

import (
	"io"
	"testing"
	"time"
)

func TestTimeoutReader(t *testing.T) {
	reader, writer := io.Pipe()
	r := newTimeoutReader(reader)

	r.SetDeadline(time.Now().Add(time.Millisecond))
	n, err := r.Read(make([]byte, 10))
	if err != ErrTimeout {
		t.Fatal("expected ErrTimeout")
	}
	if n != 0 {
		t.Fatalf("expected: 0\ngot: %d", n)
	}

	writer.Write([]byte("hello"))
	r.SetDeadline(time.Now().Add(time.Millisecond))
	buf := make([]byte, 5)
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected: 5\ngot: %d", n)
	}
	if string(buf) != "hello" {
		t.Fatalf("expected: hello\ngot: %s", buf)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("error: %v", err)
	}

	n, err = r.Read(make([]byte, 5))
	if err != io.ErrClosedPipe {
		t.Fatalf("expected: ErrClosedPipe\ngot: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected: 0\ngot: %d", n)
	}
}

// TestTimeoutReaderBlockingRead tests reading with zero deadline (blocking mode).
func TestTimeoutReaderBlockingRead(t *testing.T) {
	reader, writer := io.Pipe()
	r := newTimeoutReader(reader)

	// Don't set deadline - this should block until data is available
	done := make(chan struct{})
	var readN int
	var readErr error
	var readBuf = make([]byte, 5)

	go func() {
		readN, readErr = r.Read(readBuf)
		close(done)
	}()

	// Write data after a short delay
	time.Sleep(10 * time.Millisecond)
	writer.Write([]byte("world"))

	// Wait for read to complete
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for blocking read")
	}

	if readErr != nil {
		t.Fatalf("error: %v", readErr)
	}
	if readN != 5 {
		t.Fatalf("expected: 5\ngot: %d", readN)
	}
	if string(readBuf) != "world" {
		t.Fatalf("expected: world\ngot: %s", readBuf)
	}

	writer.Close()
	r.Close()
}

// TestTimeoutReaderReadWithBufferedData tests reading when data is already buffered.
func TestTimeoutReaderReadWithBufferedData(t *testing.T) {
	reader, writer := io.Pipe()
	r := newTimeoutReader(reader)

	// Write data first
	go func() {
		writer.Write([]byte("buffered"))
	}()

	// Small delay to ensure data is written
	time.Sleep(10 * time.Millisecond)

	// Read with zero deadline - should return immediately if buffered
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if n != 8 {
		t.Fatalf("expected: 8\ngot: %d", n)
	}
	if string(buf) != "buffered" {
		t.Fatalf("expected: buffered\ngot: %s", buf)
	}

	writer.Close()
	r.Close()
}

// TestTimeoutReaderBlockingReadThenTimeout tests the blocking read that gets data quickly.
func TestTimeoutReaderBlockingReadThenTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	r := newTimeoutReader(reader)

	// First, write some data and read it with a deadline (polls from channel)
	go func() {
		time.Sleep(5 * time.Millisecond)
		writer.Write([]byte("fast"))
	}()

	// Set a deadline far in the future
	r.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected: 4\ngot: %d", n)
	}

	writer.Close()
	r.Close()
}
