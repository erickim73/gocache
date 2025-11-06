package lru

type Node struct {
	key string
	prev *Node
	next *Node
}

type LRU struct {
	head *Node
	tail *Node
}

func New(maxSize int) *LRU {
	return &LRU{
		head: nil,
		tail: nil,
	}
}

// Removes node from the doubly linked list
func (lru *LRU) RemoveNode(node *Node) {
	// Only node in the list
	if lru.head == node && lru.tail == node { 
		lru.head = nil
		lru.tail = nil
		return
	} 
	
	// Node is at head
	if lru.head == node { 
		lru.head = node.next
		node.next.prev = nil
		return
	} 
		
	// Node is at tail
	if lru.tail == node { 
		lru.tail = node.prev
		node.prev.next = nil
		return
	} 
	
	// Node is in middle
	node.prev.next = node.next
	node.next.prev = node.prev
}

// Adds a node to the front of the list
func (lru *LRU) addToFront(node *Node) {
	// Empty list
	if lru.head == nil && lru.tail == nil { 
		lru.head = node
		lru.tail = node
		node.next = nil
		node.prev = nil
		return
	} 

	// Non-empty list
	node.prev = nil
	node.next = lru.head
	lru.head.prev = node
	lru.head = node
}

// Moves an existing node to the front of the list
func (lru *LRU) MoveToFront(node *Node) {
	// Node is at head
	if lru.head == node { 
		return
	} 

	lru.RemoveNode(node)
	lru.addToFront(node)
}

// Removes the node at the tail of the list
func (lru *LRU) RemoveLRU() string {
	if lru.tail == nil {
		return ""
	}
	
	key := lru.tail.key
	lru.RemoveNode(lru.tail)

	return key
}

// Creates a new node for key and adds it to the front
func (lru *LRU) Add(key string) *Node {
	newNode := &Node{
		key: key,
		prev: nil,
		next: nil,
	}

	lru.addToFront(newNode)
	
	return newNode
}