package protocol

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

func Parse(r *bufio.Reader) (interface{}, error) {
	first, err := r.ReadByte()

	if err != nil {
		return nil, err
	}

	switch first {
	case '+':
		return parseSimpleString(r)
	case '-':
		return parseError(r)
	case ':':
		return parseInteger(r)
	case '$':
		return parseBulkString(r)
	case '*':
		return parseArray(r)
	default:
		return nil, errors.New("unknown RESP type")
	}
}

// Helper function for reading a string line. Reads a line and validates \r\n
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}

	// check if it has \r\n
	if !strings.HasSuffix(line, "\r\n") {
		return "", errors.New("invalid RESP: missing \\r\\n")
	}

	// remove \r\n
	return strings.TrimSuffix(line, "\r\n"), nil
}

// Parses simple string. Expects "+[string]\r\n"
func parseSimpleString(r *bufio.Reader) (string, error) {
	return readLine(r)
}

// Parses Error Messages. Expects "-[Error]\r\n"
func parseError(r *bufio.Reader) (string, error) {
	return readLine(r)
}

// Parses Integers. Expects ":[num]\r\n"
func parseInteger(r *bufio.Reader) (int, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}

	// check if it has \r\n
	if !strings.HasSuffix(line, "\r\n") {
		return 0, errors.New("invalid RESP: missing \\r\\n")
	}

	// remove \r\n
	strNum := strings.TrimSuffix(line, "\r\n")

	// convert int --> string
	num, err := strconv.Atoi(strNum)
	if err != nil {
		return 0, errors.New("invalid RESP: not a valid integer")
	}
	return num, nil
}

// Parses Bulk Strings. Expects "$[length]\r\n[string]\r\n"
func parseBulkString(r *bufio.Reader) (string, error) {
	// extract length
	lengthStr, err := readLine(r)
	if err != nil {
		return "", err
	}

	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return "", errors.New("invalid bulk string length")
	}

	// handle null
	if length == -1 {
		return "", nil
	}

	// read exactly n bytes
	data := make([]byte, length)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return "", err
	}

	// consume trailing \r\n
	cr, err := r.ReadByte()
	if err != nil || cr != '\r' {
		return "", errors.New("expected \\r after bulk string data")
	}	

	lf, err := r.ReadByte()
	if err != nil || lf != '\n' {
		return "", errors.New("expected \\n after bulk string data")
	}

	return string(data), nil
}

// Parse Arrays. Expects '*[length]\r\n[array]\r\n'
func parseArray(r *bufio.Reader) ([]interface{}, error) {
	// extract length
	lengthStr, err := readLine(r)
	if err != nil {
		return nil, err
	}

	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return nil, errors.New("invalid array length")
	}

	// handle null
	if length == -1 {
		return nil, nil
	}

	// create an empty res array
	res := make([]interface{}, 0, length)

	// parse each element
	for i := 0; i < length; i++ {
		element, err := Parse(r)
		if err != nil {
			return nil, err
		}
		res = append(res, element)
	}

	return res, nil
}