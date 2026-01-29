package main

import (
	"net"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/pkg/protocol"
	
)

// routes CLUSTER subcommands to appropriate handles
func handleClusterCommand(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *server.NodeState) {
	if len(command) < 2 {
		conn.Write([]byte(protocol.EncodeError("CLUSTER command requires subcommand")))
		return
	}

	// extract and parse subcommand (ADDNODE, REMOVENODE, NODES)
	subcommand := command[1].(string)

	// route to specific handles based on subcommand
	switch subcommand {
	case "ADDNODE":
		handleClusterAddNode(conn, command, cache, nodeState)
	case "REMOVENODE":
		handleClusterRemoveNode(conn, command, cache, nodeState)
	case "NODES":
		handleClusterNodes(conn, nodeState)
	default:
		conn.Write([]byte(protocol.EncodeError("Unknown CLUSTER subcommand")))
	}
}

