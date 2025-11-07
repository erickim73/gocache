package protocol

import (
	"strconv"
)

// Encodes a simple string s to "+s\r\n"
func EncodeSimpleString(s string) string {
	return "+" + s + "\r\n"
}

// Encodes a Bulk String to "#[length]\r\n[string]\r\n"
func EncodeBulkString(s string, isNull bool) string {
	if isNull {
		return "$-1\r\n"
	}

	var length string = strconv.Itoa(len(s))
	return "$" + length + "\r\n" + s + "\r\n"
}

// Encodes a int to ":[num]\r\n"
func EncodeInteger(n int) string {
	return ":" + strconv.Itoa(n) + "\r\n"
}

// Encodes an error message into "-[Error]\r\n"
func EncodeError(s string) string {
	return "-" + s + "\r\n"
}

// Encodes an array into "*[num of elements]\r\n[element1]"
func EncodeArray(elements []string) string {
	result := "*" + strconv.Itoa(len(elements)) + "\r\n"
	
	for _, value := range elements {
		result += EncodeBulkString(value, false)
	}
	
	return result
}