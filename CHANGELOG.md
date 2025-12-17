# Changelog

## v0.1.0 (Unreleased)

### Breaking Changes
- Go version requirement: 1.12 → 1.25.3

### Dependencies Updated
| Package | Before | After |
|---------|--------|-------|
| `github.com/hashicorp/raft` | v1.1.2 | v1.7.3 |
| `github.com/hashicorp/go-hclog` | v0.12.0 | v1.6.2 |
| `github.com/nats-io/nats-server/v2` | v2.1.4 | v2.10.0 |
| `github.com/nats-io/nats.go` | v1.9.1 | v1.31.0 |

### Code Changes
- Removed custom `min()` function (now builtin in Go 1.21+)
- Fixed pipeline test for raft v1.7.3 compatibility: the test now consumes
  pipeline responses concurrently to work with raft's default `maxInFlight=2`
  buffer size, preventing pipeline backpressure deadlocks
- Improved test coverage from 77.4% to 84.7% with new tests for edge cases

### Raft v1.7.3 Compatibility
This release enables compatibility with hashicorp/raft v1.7.3, which includes:
- Pre-vote protocol (enabled by default)
- go-msgpack v2 upgrade
- CommitIndex API
- Various performance and stability improvements
