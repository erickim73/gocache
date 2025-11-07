package protocol

import (
	"testing"
	"bufio"
	"strings"
)

func TestEncodeSimpleString(t *testing.T) {
	result := EncodeSimpleString("OK")
	expected := "+OK\r\n"

	if result != expected {
		t.Errorf("Encoding simple string got %s, expected %s", result, expected)
	}
}

func TestEncodeBulkString(t *testing.T) {
	result := EncodeBulkString("Hello World", false)
	expected := "$11\r\nHello World\r\n"

	if result != expected {
		t.Errorf("Encoding bulk string got %s, expected %s", result, expected)
	}

	result2 := EncodeBulkString("", false)
	expected2 := "$0\r\n\r\n"

	if result2 != expected2 {
		t.Errorf("Encoding bulk string got %s, expected %s", result, expected)
	}
	
	result3 := EncodeBulkString("", true)
	expected3 := "$-1\r\n"
	
	if result3 != expected3 {
		t.Errorf("Encoding bulk string got %s, expected %s", result, expected)
	}
}

func TestEncodeInteger(t *testing.T) {
	result := EncodeInteger(12)
	expected := ":12\r\n"

	if result != expected {
		t.Errorf("Encoding integer got %s, expected %s", result, expected)
	}
}

func TestEncodeError(t *testing.T) {
	result := EncodeError("ERR unknown command 'abc'")
	expected := "-ERR unknown command 'abc'\r\n"
	
	if result != expected {
		t.Errorf("Encoding error message got %s, expected %s", result, expected)
	}
}

func TestParseSimpleString(t *testing.T) {
	input := "+OK\r\n"
	reader := bufio.NewReader(strings.NewReader(input))
	result, err := Parse(reader)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	str, ok := result.(string)
	if !ok {
		t.Fatal("Expected result to be a string")
	}

	if str != "OK" {
		t.Errorf("Expected 'OK', got %s", str)
	}
}

func TestParseError(t *testing.T) {
	input := "-ERR unknown command 'abc'\r\n"
	reader := bufio.NewReader(strings.NewReader(input))
	result, err := Parse(reader)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	str, ok := result.(string)
	if !ok {
		t.Fatal("Expected result to be a string")
	}

	if str != "ERR unknown command 'abc'" {
		t.Errorf("Expected ERR unknown command 'abc', got %s", str)
	}
}