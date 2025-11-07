package protocol

import (
	"testing"
)

func TestEncodeSimpleString(t *testing.T) {
	result := EncodeSimpleString("OK")
	expected := "+OK\r\n"

	if result != expected {
		t.Errorf("Encoding simple string got %s, expected %s", result, expected)
	}
}

func TestEncodeBulkString(t *testing.T) {
	result := EncodeBulkString("Hello World")
	expected := "$11\r\nHello World\r\n"

	if result != expected {
		t.Errorf("Encoding bulk string got %s, expected %s", result, expected)
	}
}