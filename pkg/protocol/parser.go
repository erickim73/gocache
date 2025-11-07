package protocol

import (
	"bufio"
	"errors"
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
	default:
		return nil, errors.New("unknown RESP type")
	};
}

// Parses simple string. Expects "+[string]\r\n"
func parseSimpleString(r *bufio.Reader) (string, error) {
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

// Parses Error Messages. Expects "-[Error]\r\n"
func parseError(r *bufio.Reader) (string, error) {
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