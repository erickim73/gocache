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
		t.Errorf("Expected 'ERR unknown command 'abc'', got %s", str)
	}
}

func TestParseInteger(t *testing.T) {
	input := ":12\r\n"
	reader := bufio.NewReader(strings.NewReader(input))
	result, err := Parse(reader)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	integer, ok := result.(int)
	if !ok {
		t.Fatal("Expected result to be a integer")
	}

	if integer != 12 {
		t.Errorf("Expected 12, got %d", integer)
	}
}

func TestParseBulkString(t *testing.T) {
	input := "$11\r\nHello World\r\n"
	reader := bufio.NewReader(strings.NewReader(input))
	result, err := Parse(reader)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	str, ok := result.(string)
	if !ok {
		t.Fatal("Expected result to be a string")
	}

	if str != "Hello World" {
		t.Errorf("Expected 'Hello World', got %s", str)
	}
}

func TestParseArray(t *testing.T) {
	input := "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"
	reader := bufio.NewReader(strings.NewReader(input))
	result, err := Parse(reader)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	arr, ok := result.([]interface{})
	if !ok {
		t.Fatal("Expected result to be a slice")
	}

	// check length of array
	if len(arr) != 2 {
		t.Errorf("Expected 2 elements, got %d", len(arr))
	}

	// check first element
	if arr[0].(string) != "foo" {
		t.Errorf("Expected first element to be 'foo', got %v", arr[0])
	}

	// check second element
	if arr[1].(string) != "bar" {
		t.Errorf("Expected second element to be 'bar', got %v", arr[1])
	}
}