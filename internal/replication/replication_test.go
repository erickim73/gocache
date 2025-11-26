package replication

import (
	"testing"
)

func TestReplicateCommandRoundTrip(t *testing.T) {
	SetCmd := &ReplicateCommand{
		SeqNum: 42,
		Operation: OpSet,
		Key: "testing",
		Value: "value",
		TTL: 3600,
	}

	encoded, err := EncodeReplicateCommand(SetCmd)
	if err != nil {
		t.Fatalf("Error Encoding Replicate Command: %v", err)
	}
	decoded, err := DecodeReplicateCommand(encoded)
	if err != nil {
		t.Fatalf("Error Decoding Replicate Command: %v", err)
	}

	if decoded.SeqNum != 42 {
		t.Errorf("SeqNum should be 42, got %d", decoded.SeqNum)
	}
	if decoded.Operation != OpSet {
		t.Errorf("Operation should be SET, got %s", decoded.Operation)
	}
	if decoded.Key != "testing" {
		t.Errorf("Key should be testing, got %s", decoded.Key)
	}
	if decoded.Value != "value" {
		t.Errorf("Value should be value, got %s", decoded.Value)
	}

	// test delete
	delCmd := &ReplicateCommand{
		SeqNum: 43,
		Operation: OpDelete,
		Key: "testing",
	}

	encoded2, err := EncodeReplicateCommand(delCmd)
	if err != nil {
		t.Fatalf("Error Encoding Replicate Command: %v", err)
	}
	decoded2, err := DecodeReplicateCommand(encoded2)
	if err != nil {
		t.Fatalf("Error Decoding Replicate Command: %v", err)
	}


	if decoded2.SeqNum != 43 {
		t.Errorf("SeqNum should be 43, got %d", decoded2.SeqNum)
	}
	if decoded2.Operation != OpDelete {
		t.Errorf("Operation should be DELETE, got %s", decoded2.Operation)
	}
	if decoded2.Key != "testing" {
		t.Errorf("Key should be 'testing', got %s", decoded2.Key)
	}
	if decoded2.Value != "" {
		t.Errorf("Delete command Value should be empty, got %s", decoded2.Value)
	}
}

func TestHeartbeatCommandRoundTrip(t *testing.T) {
	HeartbeatCmd := &HeartbeatCommand{
		SeqNum: 44, 
		NodeID: "101",
	}

	encoded, err := EncodeHeartbeatCommand(HeartbeatCmd)
	if err != nil {
		t.Fatalf("Error Encoding Heartbeat Command: %v", err)
	}
	decoded, err := DecodeHeartbeatCommand(encoded)
	if err != nil {
		t.Fatalf("Error Decoding Heartbeat Command: %v", err)
	}

	if decoded.SeqNum != 44 {
		t.Errorf("SeqNum should be 42, got %d", decoded.SeqNum)
	}
	if decoded.NodeID != "101" {
		t.Errorf("NodeID should be 42, got %s", decoded.NodeID)
	}
}