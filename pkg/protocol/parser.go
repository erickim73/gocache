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
	default:
		return nil, errors.New("unknown RESP type")
	};
}

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