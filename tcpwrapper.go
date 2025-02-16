package tcpwrapper

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	isrequest "github.com/mmskazak/tcpwrapper/is_request"
	isresponse "github.com/mmskazak/tcpwrapper/is_response"
)

// Middleware defines a type of middleware function for processing messages.
type Middleware func([]byte) ([]byte, error)

// TCPWrapper is a wrapper over a TCP connection that allows
// applying different middleware chains for processing requests and responses.
type TCPWrapper struct {
	net.Conn
	RequestDelimiter    []byte
	ResponseDelimiter   []byte
	RequestMiddlewares  []Middleware
	ResponseMiddlewares []Middleware
	isRequest           isrequest.IsRequestFunc
	isResponse          isresponse.IsResponseFunc
}

// NewTCPWrapper creates a new instance of TCPWrapper with the given connection and delimiters.
func NewTCPWrapper(
	conn net.Conn,
	requestDelimiter,
	responseDelimiter []byte,
	isRequest isrequest.IsRequestFunc,
	isResponse isresponse.IsResponseFunc,
) *TCPWrapper {
	return &TCPWrapper{
		Conn:                conn,
		RequestDelimiter:    requestDelimiter,
		ResponseDelimiter:   responseDelimiter,
		RequestMiddlewares:  make([]Middleware, 0),
		ResponseMiddlewares: make([]Middleware, 0),
		isRequest:           isRequest,
		isResponse:          isResponse,
	}
}

// AddRequestMiddleware adds a middleware for request processing.
func (tw *TCPWrapper) AddRequestMiddleware(mw Middleware) {
	tw.RequestMiddlewares = append(tw.RequestMiddlewares, mw)
}

// AddResponseMiddleware adds a middleware for response processing.
func (tw *TCPWrapper) AddResponseMiddleware(mw Middleware) {
	tw.ResponseMiddlewares = append(tw.ResponseMiddlewares, mw)
}

// readMessage reads data from the connection until one of the following conditions is met:
// 1. If a Content-Length header is found, reads the specified number of bytes.
// 2. If an explicit delimiter is detected, considers the message complete.
// 3. If EOF is received, returns the accumulated data.
func (tw *TCPWrapper) readMessage(delimiter []byte) ([]byte, error) {
	var buffer []byte
	temp := make([]byte, 256)
	expectedLength := -1

	for {
		n, err := tw.Conn.Read(temp)
		if err != nil && err != io.EOF {
			return nil, err
		}
		buffer = append(buffer, temp[:n]...)

		// If expected length is not set, try to extract Content-Length from headers.
		if expectedLength == -1 {
			// Assume headers end with \r\n\r\n
			if headerEnd := bytes.Index(buffer, []byte("\r\n\r\n")); headerEnd != -1 {
				headers := buffer[:headerEnd]
				if cl, err := extractContentLength(headers); err == nil {
					// Final length = headers + 4 bytes (\r\n\r\n) + body length
					expectedLength = headerEnd + 4 + cl
				}
			}
		}

		if expectedLength != -1 && len(buffer) >= expectedLength {
			break
		}

		if len(delimiter) > 0 && bytes.HasSuffix(buffer, delimiter) {
			break
		}

		if err == io.EOF {
			break
		}
	}

	return buffer, nil
}

// HandleMessage reads a complete message, determines its type (response or request),
// and runs the corresponding middleware chain before sending the result back.
func (tw *TCPWrapper) HandleMessage() error {
	// Use RequestDelimiter to read the message.
	message, err := tw.readMessage(tw.RequestDelimiter)
	if err != nil {
		return err
	}

	// Use the provided isRequest and isResponse functions to determine message type
	if tw.isRequest(message) {
		for _, mw := range tw.RequestMiddlewares {
			message, err = mw(message)
			if err != nil {
				return err
			}
		}
	} else if tw.isResponse(message) {
		for _, mw := range tw.ResponseMiddlewares {
			message, err = mw(message)
			if err != nil {
				return err
			}
		}
	}

	_, err = tw.Conn.Write(message)
	return err
}

// Close properly closes the connection.
func (tw *TCPWrapper) Close() error {
	return tw.Conn.Close()
}

// extractContentLength searches for the "Content-Length" header in headers and returns its value.
// If not found, returns an error.
func extractContentLength(headers []byte) (int, error) {
	lines := strings.Split(string(headers), "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				clStr := strings.TrimSpace(parts[1])
				return strconv.Atoi(clStr)
			}
		}
	}
	return 0, fmt.Errorf("Content-Length not found")
}
