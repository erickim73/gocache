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

	encoded := EncodeReplicateCommand(SetCmd)
	decoded, err := DecodeReplicateCommand(encoded)
	if err != nil {
		t.Fatalf("Error Decodeing Replicate Command: %v", err)
	}

	var decodedCmd *ReplicateCommand
	if dc, ok := decoded.(*ReplicateCommand); ok {
		decodedCmd = dc
	} else if dcVal, ok := decoded.(ReplicateCommand); ok {
		decodedCmd = &dcVal
	} else {
		t.Fatalf("Decoded command is not a ReplicateCommand, got %T", decoded)
	}

	if decodedCmd.SeqNum != 42 {
		t.Errorf("SeqNum should be 42, got %d", decodedCmd.SeqNum)
	}
	if decodedCmd.Operation != OpSet {
		t.Errorf("Operation should be SET, got %s", decodedCmd.Operation)
	}
	if decodedCmd.Key != "testing" {
		t.Errorf("Key should be testing, got %s", decodedCmd.Key)
	}
	if decodedCmd.Value != "value" {
		t.Errorf("Value should be value, got %s", decodedCmd.Value)
	}
}