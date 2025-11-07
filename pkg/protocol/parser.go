package protocol

import (
	"bufio"
	"errors"
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
	num, err := strconv.Atoi(strNum)
	if err != nil {
		return 0, errors.New("invalid RESP: not a valid integer")
	}
	return num, nil
}