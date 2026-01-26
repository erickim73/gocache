package protocol

import (
	"fmt"
	"strconv"
	"strings"
)


type RedirectResponse struct {
	LeaderHost string
	LeaderPort string
}

// Encodes a simple string to "+[string]\r\n"
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
func EncodeArray(elements []interface{}) string {
	result := "*" + strconv.Itoa(len(elements)) + "\r\n"
	
	for _, value := range elements {
		// handle different types
		switch v := value.(type) {
		case string:
			result += EncodeBulkString(v, false)
		case int:
			result += EncodeInteger(v)
		case int64:
			result += EncodeInteger(int(v))
		default:
			panic("unsupported type in array")
			
		}
	}

	return result
}

// encodes a redirect response in RESP format
func EncodeRedirect(host string, port int) string {
	return fmt.Sprintf("-MOVED %s:%d\r\n", host, port)
}

// function to detect if a response is a redirect
func IsRedirect(response string) bool {
	return strings.HasPrefix(response, "-MOVED")
}

// parses a redirect response into host and port components. Format: "-MOVED host:port\r\n". Returns error if format is invalid
func ParseRedirect(response string) (*RedirectResponse, error) {
	// validate that it's actually a redirect
	if !IsRedirect(response) {
		return nil, fmt.Errorf("not a redirect response: %s", response)
	}

	// remove "-MOVED "
	withoutPrefix := strings.TrimPrefix(response, "-MOVED ")

	// remove "\r\n"
	withoutSuffix := strings.TrimSuffix(withoutPrefix, "\r\n")

	// split on ":" to separate host and port
	parts := strings.Split(withoutSuffix, ":")

	// validate that we got 2 parts: host and port
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid redirect format, expected 'host:port' but got: %s", withoutSuffix)
	}

	host := parts[0]
	port := parts[1]

	// validate host isn't empty
	if host == "" {
		return nil, fmt.Errorf("invalid redirect: empty host")
	}

	// validate port isn't empty
	if port == "" {
		return nil, fmt.Errorf("invalid redirect: empty port")
	}

	// validate port is a number
	_, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("invalid redirect: port must be a number got %s", port)
	}

	// return parsed redirect response
	return &RedirectResponse{
		LeaderHost: host,
		LeaderPort: port,
	}, nil
}

// returns full address as "host:port" string
func (r *RedirectResponse) Address() string {
	return fmt.Sprintf("%s:%s", r.LeaderHost, r.LeaderPort)
}