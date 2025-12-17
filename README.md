# nats-on-a-log

[![Go Reference](https://pkg.go.dev/badge/github.com/liftbridge-io/nats-on-a-log.svg)](https://pkg.go.dev/github.com/liftbridge-io/nats-on-a-log)
[![Coverage](https://img.shields.io/badge/coverage-84.7%25-brightgreen)](coverage.out)
[![Go Version](https://img.shields.io/badge/go-1.25.3-blue)](https://go.dev/)

This library provides a [NATS](https://github.com/nats-io/gnatsd) transport for HashiCorp's [Raft](https://github.com/hashicorp/raft) implementation.

## Maintainers

This project is maintained by [Basekick Labs](https://github.com/basekick-labs), creators of [Arc](https://github.com/basekick-labs/arc).

## Requirements

- Go 1.25.3+
- hashicorp/raft v1.7.3+
- NATS Server v2.10.0+

## Installation

```bash
go get github.com/liftbridge-io/nats-on-a-log
```

## Usage

```go
import (
    natslog "github.com/liftbridge-io/nats-on-a-log"
    "github.com/nats-io/nats.go"
)

// Connect to NATS
nc, err := nats.Connect(nats.DefaultURL)
if err != nil {
    log.Fatal(err)
}

// Create NATS transport for Raft
transport, err := natslog.NewNATSTransport(
    "node-1",           // Node ID
    "raft.",            // Subject prefix
    nc,                 // NATS connection
    2*time.Second,      // Timeout
    os.Stderr,          // Log output
)
if err != nil {
    log.Fatal(err)
}
defer transport.Close()

// Use transport with Raft configuration
raftConfig := raft.DefaultConfig()
raftConfig.LocalID = "node-1"

// ... create Raft instance with transport
```

## Features

- NATS-based transport layer for Raft consensus
- Supports Raft v1.7.3 features including pre-vote protocol
- Connection pooling via NATS subscriptions
- Configurable timeouts and logging

## Test Coverage

| Package | Coverage |
|---------|----------|
| nats-on-a-log | 84.7% |

Run tests with coverage:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

## License

Apache License 2.0
