# GoCache Python Client

A pure-Python client library for [GoCache](../../../README.md) — a Redis-compatible in-memory cache server. No external dependencies. Python 3.10+ only.

---

## Installation

No package manager needed. Copy `client.py` into your project and import from it directly.

```bash
# Clone the repo
git clone https://github.com/erickim73/gocache.git
cd gocache

# Verify Python version (3.10+ required)
python --version
```

That's it. `client.py` uses only the Python standard library (`socket`, `time`).

---

## Running the Server

Before using the client, start the GoCache server:

```bash
# From the repo root
go run cmd/server/main.go

# Or if you've built the binary
./bin/gocache
```

The server listens on `localhost:6379` by default.

---

## Quick Start

```python
from client import GoCacheClient

with GoCacheClient("localhost", 6379) as cache:
    # Health check
    cache.ping()                          # → 'PONG'

    # Store and retrieve a value
    cache.set("user:1000", "Eric")        # → 'OK'
    cache.get("user:1000")                # → 'Eric'

    # Missing keys return None, not an error
    cache.get("user:9999")                # → None

    # Set a key that expires after 60 seconds
    cache.set("session:token", "abc123", ex=60)

    # Delete one or more keys
    cache.delete("user:1000")             # → 1  (number of keys removed)
    cache.delete("k1", "k2", "k3")        # → 3
```

The `with` statement guarantees the TCP connection is closed when the block exits, even if an exception occurs. For long-lived processes, you can also manage the lifecycle manually:

```python
cache = GoCacheClient("localhost", 6379)
cache.set("key", "value")
cache.close()
```

---

## API Reference

### `GoCacheClient(host, port)`

Opens a TCP connection to the GoCache server. Raises `GoCacheConnectionError` if the server is not reachable.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `host` | `str` | `"localhost"` | Server hostname or IP address |
| `port` | `int` | `6379` | Server port |

```python
client = GoCacheClient("localhost", 6379)
client = GoCacheClient("10.0.0.5", 6380)   # custom host and port
```

---

### `ping() → str`

Sends a PING to the server. Returns `'PONG'` on a healthy connection. Use this to verify the server is reachable before issuing commands.

```python
response = client.ping()   # → 'PONG'
```

---

### `get(key) → str | None`

Retrieves the value stored at `key`. Returns `None` if the key does not exist — it does not raise an exception.

| Parameter | Type | Description |
|-----------|------|-------------|
| `key` | `str` | The key to look up |

**Returns:** `str` if the key exists, `None` if it does not.

```python
client.set("color", "blue")

client.get("color")       # → 'blue'
client.get("missing")     # → None
```

---

### `set(key, value, ex=None) → str`

Stores `value` at `key`. Overwrites any existing value. Returns `'OK'` on success.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `key` | `str` | — | The key to write |
| `value` | `str` | — | The value to store |
| `ex` | `int \| None` | `None` | Optional TTL in seconds. The key is deleted automatically after this many seconds. |

**Returns:** `'OK'`

```python
client.set("name", "Eric")              # → 'OK'  (persists until deleted)
client.set("session", "xyz", ex=3600)   # → 'OK'  (expires after 1 hour)
```

---

### `delete(*keys) → int`

Deletes one or more keys. Keys that do not exist are silently ignored and do not affect the count.

| Parameter | Type | Description |
|-----------|------|-------------|
| `*keys` | `str` | One or more keys to delete |

**Returns:** `int` — the number of keys that were actually deleted (keys that did not exist count as 0).

```python
client.set("a", "1")
client.set("b", "2")

client.delete("a")               # → 1
client.delete("b", "c", "d")     # → 1  ("c" and "d" didn't exist)
client.delete("already_gone")    # → 0
```

---

### `close() → None`

Closes the TCP connection and releases the socket. After calling this, any further method calls on the client will raise an `OSError`.

If you use the client as a context manager (`with GoCacheClient(...) as c:`), `close()` is called automatically when the block exits. You only need to call it manually if you're managing the lifecycle yourself.

```python
client = GoCacheClient("localhost", 6379)
client.set("key", "value")
client.close()
```

---

### Context Manager Support

`GoCacheClient` implements `__enter__` and `__exit__`, so it can be used as a context manager. This is the recommended usage pattern — it guarantees the socket is closed regardless of whether the block exits normally or via an exception.

```python
with GoCacheClient("localhost", 6379) as c:
    c.set("key", "value")
    value = c.get("key")
# Socket is closed here automatically
```

---

## Error Handling

The client defines three exception types, all in `client.py`:

### `GoCacheError`

Base class for all GoCache-specific errors. Catch this if you want to handle any GoCache error in one place.

```python
from client import GoCacheError

try:
    client.set("key", "value")
except GoCacheError as e:
    print(f"Something went wrong: {e}")
```

---

### `GoCacheConnectionError(GoCacheError)`

Raised when the client cannot connect to the server, or when the connection is lost mid-command.

```python
from client import GoCacheConnectionError

try:
    client = GoCacheClient("localhost", 19999)   # nothing listening here
except GoCacheConnectionError as e:
    print(e)
    # → Could not connect to GoCache at localhost:19999. Is the server running?
```

---

### `GoCacheCommandError(GoCacheError)`

Raised when the server returns a RESP error response (a `-` type message). This indicates the server understood the command but rejected it — for example, an unknown command name or wrong number of arguments.

```python
from client import GoCacheCommandError

try:
    client._send("NOT_A_COMMAND")
    client._read_response()
except GoCacheCommandError as e:
    print(e)   # → ERR unknown command 'NOT_A_COMMAND'
```

---

## Running the Examples

```bash
cd pkg/client/python

# Make sure GoCache is running first
go run ../../../cmd/server/main.go &

python examples.py
```

Expected output:

```
──────────────────────────────────────────────────
  PING — health check
──────────────────────────────────────────────────
  ping()  →  'PONG'
  ✓ server is reachable

──────────────────────────────────────────────────
  SET / GET — basic read-write
──────────────────────────────────────────────────
  set('user:1000:name', 'Eric')  →  'OK'
  get('user:1000:name')          →  'Eric'
  ...
```

---

## Running the Tests

```bash
cd pkg/client/python

# Unit tests only — no server required
python -m unittest test_client.TestRespEncoder test_client.TestRespParser -v

# All tests — integration tests run if GoCache is reachable, skip otherwise
python -m unittest test_client -v
```

The test suite has two layers:

- **Unit tests** (`TestRespEncoder`, `TestRespParser`) — 24 tests covering the RESP encoder and parser in isolation. No server needed.
- **Integration tests** (`TestGoCacheClient`) — 21 tests exercising every method against a live server. Skipped automatically with a clear message if the server is not running.

---

## File Structure

```
pkg/client/python/
├── client.py        # Client library — all code lives here
├── examples.py      # End-to-end demo script
├── test_client.py   # Unit and integration tests
└── README.md        # This file
```

---

## Compatibility

| | Requirement |
|-|-------------|
| Python | 3.10+ |
| GoCache server | any version supporting RESP protocol |
| Dependencies | none (standard library only) |