package cluster

import (

)

// represents the health state of a node
type NodeStatus int

const (
	NodeStatusAlive NodeStatus = iota
	NodeStatusSuspected // missed some pings but not confirmed dead
	NodeStatusDead // confirmed dead after multiple failures
)

func (ns NodeStatus) String() string {
	switch ns {
	case NodeStatusAlive:
		return "alive"
	case NodeStatusSuspected:
		return "suspected"
	case NodeStatusDead:
		return "dead"
	default:
		return "unknown"
	}
}

