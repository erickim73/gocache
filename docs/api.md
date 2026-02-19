# GoCache API Reference

GoCache implements the [Redis Serialization Protocol (RESP)](https://redis.io/docs/latest/). Any Redis client library or `redis-cli` will work without modification.

---

## Core Operations

### SET

**Syntax**
```
SET key value [EX seconds]
```

**Description**  
Stores a string value at the given key. Overwrites any existing value. If `EX seconds` is provided, the key expires and is automatically deleted after that many seconds. Writes are only accepted on leader nodes.

**Returns**
- `+OK` on success
- `-ERR READONLY You can't write against a read only replica` if sent to a follower
- `-ERR syntax error` if a qualifier other than `EX` is provided
- `-ERR value is not an integer` if the TTL argument is not a valid integer
- `-MOVED <node_id> <addr>` if the key belongs to a different cluster node

**Example**
```bash
# No TTL
redis-cli -p 7000 SET username "eric"
# OK

# With TTL
redis-cli -p 7000 SET session:abc "data" EX 3600
# OK

# On a follower
redis-cli -p 7002 SET username "eric"
# (error) ERR READONLY You can't write against a read only replica
```

---

### GET

**Syntax**
```
GET key
```

**Description**  
Returns the string value stored at the given key. Followers can serve GET requests.

**Returns**
- Bulk string value if the key exists
- Null bulk string (`$-1`) if the key does not exist or has expired
- `-ERR` if the wrong number of arguments is provided

**Example**
```bash
redis-cli -p 7000 GET username
# "eric"

redis-cli -p 7000 GET nonexistent
# (nil)
```

---

### DEL

**Syntax**
```
DEL key
```

**Description**  
Deletes the specified key. Writes are only accepted on leader nodes.

**Returns**
- `:1` (integer) on success
- `:0` (integer) if the key did not exist
- `-ERR READONLY You can't write against a read only replica` if sent to a follower
- `-MOVED <node_id> <addr>` if the key belongs to a different cluster node

**Example**
```bash
redis-cli -p 7000 DEL username
# (integer) 1

redis-cli -p 7000 DEL nonexistent
# (integer) 0

# On a follower
redis-cli -p 7002 DEL username
# (error) ERR READONLY You can't write against a read only replica
```

---

### DBSIZE

**Syntax**
```
DBSIZE
```

**Description**  
Returns the number of keys currently stored in the cache. Includes all non-expired keys.

**Returns**
- Integer reply with the key count

**Example**
```bash
redis-cli -p 7000 DBSIZE
# (integer) 42
```

---

### PING

**Syntax**
```
PING
```

**Description**  
Health check command. Always responds immediately. Allowed in subscriber mode and during transactions (though in the latter it will be queued and executed on `EXEC`).

**Returns**
- `+PONG`

**Example**
```bash
redis-cli -p 7000 PING
# PONG
```

---

## Transactions

Transactions allow a sequence of commands to be queued and executed atomically. No other client's commands will be interleaved during execution.

**Transaction lifecycle**
```
MULTI                  → begin queuing
SET / GET / DEL / ...  → each returns +QUEUED
EXEC                   → execute all queued commands, return array of results
```

If any command fails syntax validation during queuing, the entire transaction is aborted on `EXEC` with `EXECABORT`. Commands that fail at *runtime* (after `EXEC`) do not abort the transaction — remaining commands still execute.

The following commands cannot be queued inside a transaction: `MULTI`, `EXEC`, `DISCARD`, `SUBSCRIBE`, `UNSUBSCRIBE`, `PUBLISH`, `PSUBSCRIBE`, `PUNSUBSCRIBE`, `CLUSTER`.

---

### MULTI

**Syntax**
```
MULTI
```

**Description**  
Begins a transaction. All subsequent commands are queued rather than executed immediately, until `EXEC` or `DISCARD` is called. Nesting `MULTI` inside an active transaction is an error.

**Returns**
- `+OK` on success
- `-ERR MULTI calls can not be nested` if already in a transaction

**Example**
```bash
redis-cli -p 7000 MULTI
# OK
```

---

### EXEC

**Syntax**
```
EXEC
```

**Description**  
Executes all commands queued since `MULTI`. Commands run atomically — the cache lock is held for the duration of execution. Returns an array of responses in the same order as the queued commands.

If any command failed syntax validation during queuing, the entire transaction is discarded and `EXECABORT` is returned instead of results.

**Returns**
- Array of RESP responses, one per queued command, in order
- `-ERR EXEC without MULTI` if called outside a transaction
- `-EXECABORT Transaction discarded because of previous errors.` if a queued command failed validation

**Example**
```bash
redis-cli -p 7000 MULTI
# OK
redis-cli -p 7000 SET counter 1
# QUEUED
redis-cli -p 7000 GET counter
# QUEUED
redis-cli -p 7000 EXEC
# 1) OK
# 2) "1"
```

---

### DISCARD

**Syntax**
```
DISCARD
```

**Description**  
Aborts the current transaction and clears all queued commands. The connection returns to normal (non-transaction) mode.

**Returns**
- `+OK` on success
- `-ERR DISCARD without MULTI` if called outside a transaction

**Example**
```bash
redis-cli -p 7000 MULTI
# OK
redis-cli -p 7000 SET key value
# QUEUED
redis-cli -p 7000 DISCARD
# OK
```

---

## Pub/Sub

Once a connection issues `SUBSCRIBE`, it enters **subscriber mode**. In this mode, only `SUBSCRIBE`, `UNSUBSCRIBE`, `PSUBSCRIBE`, `PUNSUBSCRIBE`, `PING`, and `QUIT` are accepted. All other commands return an error until the connection unsubscribes from all channels.

`PUBLISH` is a normal-mode command — use a separate connection to publish while subscriber connections listen.

---

### SUBSCRIBE

**Syntax**
```
SUBSCRIBE channel [channel ...]
```

**Description**  
Subscribes the connection to one or more channels and enters subscriber mode. For each channel, a confirmation message is sent immediately.

**Returns**  
For each channel, a three-element array:
- `subscribe` (message type)
- channel name
- total number of channels this connection is now subscribed to

**Example**
```bash
redis-cli -p 7000 SUBSCRIBE news alerts
# 1) "subscribe"
# 2) "news"
# 3) (integer) 1
# 1) "subscribe"
# 2) "alerts"
# 3) (integer) 2
```

---

### UNSUBSCRIBE

**Syntax**
```
UNSUBSCRIBE [channel ...]
```

**Description**  
Unsubscribes from the specified channels. If no channels are given, unsubscribes from all channels and exits subscriber mode. For each channel, a confirmation message is sent.

**Returns**  
For each channel, a three-element array:
- `unsubscribe` (message type)
- channel name
- remaining number of subscriptions for this connection

**Example**
```bash
# Unsubscribe from one channel
redis-cli -p 7000 UNSUBSCRIBE news
# 1) "unsubscribe"
# 2) "news"
# 3) (integer) 1

# Unsubscribe from all channels
redis-cli -p 7000 UNSUBSCRIBE
# 1) "unsubscribe"
# 2) "alerts"
# 3) (integer) 0
```

---

### PUBLISH

**Syntax**
```
PUBLISH channel message
```

**Description**  
Publishes a message to all connections currently subscribed to the given channel. Must be called from a normal (non-subscriber) connection.

**Returns**
- Integer reply with the number of subscribers that received the message
- `-ERR wrong number of arguments for 'publish' command` if argument count is wrong
- `-ERR invalid channel name` if the channel argument is not a string
- `-ERR invalid message` if the message argument is not a string

**Example**
```bash
redis-cli -p 7000 PUBLISH news "breaking: gocache ships docs"
# (integer) 3
```

---

## Cluster

CLUSTER subcommands manage node membership, data migration, and topology state at runtime. These commands cannot be queued inside a transaction.

When a client sends a command for a key that belongs to a different shard, GoCache responds with a `-MOVED` redirect rather than proxying the request:
```
-MOVED <node_id> <host:port>
```
The client is expected to reconnect to the indicated node and retry.

---

### CLUSTER ADDNODE

**Syntax**
```
CLUSTER ADDNODE <nodeID> <host:port>
```

**Description**  
Adds a new node to the cluster. Before adding, GoCache verifies the node is reachable via a test TCP connection. If reachable, it triggers a data migration — keys that consistent hashing assigns to the new node are moved from the current node. After migration, all existing cluster nodes are notified of the topology change.

**Returns**
- `+OK` on success
- `-ERR ADDNODE requires nodeID and address` if arguments are missing
- `-ERR Node <id> already exists in cluster` if the node is already a member
- `-ERR Cannot connect to node <id> at <addr>...` if the node is unreachable
- `-ERR Migration failed: <reason>` if data migration fails
- `-ERR Cluster not initialized` if the migrator is not set up

**Example**
```bash
redis-cli -p 7000 CLUSTER ADDNODE node4 localhost:7003
# OK
```

---

### CLUSTER REMOVENODE

**Syntax**
```
CLUSTER REMOVENODE <nodeID>
```

**Description**  
Removes a node from the cluster. Keys that consistent hashing assigned to the leaving node are migrated to the remaining nodes before removal. All other cluster nodes are then notified of the topology change. If the leaving node is still reachable, it is also notified to update its own hash ring.

**Returns**
- `+OK` on success
- `-ERR REMOVENODE requires nodeID` if the argument is missing
- `-ERR Migration failed: <reason>` if data migration fails
- `-ERR Cluster is not initialized` if the migrator is not set up

**Example**
```bash
redis-cli -p 7000 CLUSTER REMOVENODE node4
# OK
```

---

### CLUSTER NODES

**Syntax**
```
CLUSTER NODES
```

**Description**  
Returns a list of all nodes in the cluster with their address and current health status. Status values are `alive`, `suspected`, or `dead`, as reported by the health checker.

**Returns**
- Bulk string with one line per node in the format: `<nodeID> <host:port> <status>`
- `-ERR Cluster is not initialized` if the cluster config is not set up

**Example**
```bash
redis-cli -p 7000 CLUSTER NODES
# "node1 localhost:7000 alive
#  node2 localhost:7001 alive
#  node3 localhost:7002 suspected"
```

---

### CLUSTER TOPOLOGY

**Syntax**
```
CLUSTER TOPOLOGY <ADD|REMOVE> <nodeID> <host:port>
```

**Description**  
Internal command used by `ADDNODE` and `REMOVENODE` to propagate topology changes across all cluster nodes. Updates the local hash ring by adding or removing the specified node. Not intended for direct client use — call `ADDNODE` and `REMOVENODE` instead.

**Returns**
- `+OK` on success
- `-ERR TOPOLOGY requires operation, nodeID, and address` if arguments are missing
- `-ERR Unknown TOPOLOGY operation` if the operation is not `ADD` or `REMOVE`

**Example**
```bash
# Internally issued by ADDNODE — not for direct use
redis-cli -p 7000 CLUSTER TOPOLOGY ADD node4 localhost:7003
# OK
```

---

### CLUSTER HEALTH

**Syntax**
```
CLUSTER HEALTH
```

**Description**  
Returns the health status of all nodes as tracked by the health checker. Similar to `CLUSTER NODES` but sourced directly from the health checker's internal state rather than the config node list.

**Returns**
- Bulk string with one line per node in the format: `<nodeID> <host:port> <status>`
- `-ERR Health checker not initialized` if the health checker is not running

**Example**
```bash
redis-cli -p 7000 CLUSTER HEALTH
# "node1 localhost:7000 alive
#  node2 localhost:7001 alive
#  node3 localhost:7002 dead"
```