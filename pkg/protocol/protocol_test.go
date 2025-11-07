package protocol

import (
	"testing"
)

func TestEncodeString(t *testing.T) {
	result := EncodeString("OK")
	expected := "+OK\r\n"

	if result != expected {
		t.Errorf("Expected to get %s, got %s", expected, result)
	}
}