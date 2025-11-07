package protocol

import (
	"strconv"
)

// Encodes a simple string s to '+s\r\n'
func EncodeSimpleString(s string) string {
	return "+" + s + "\r\n"
}

// Encodes a Bulk String to #[length]\r\n[string]\r\n
func EncodeBulkString(s string) string {
	var length string = strconv.Itoa(len(s))
	return "$" + length + "\r\n" + s + "\r\n"
}