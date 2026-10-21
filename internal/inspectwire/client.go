package inspectwire

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Client is a connection to the bridge.
type Client struct {
	c net.Conn
	r *bufio.Reader
}

// Dial connects to the socket at path.
func Dial(path string, timeout time.Duration) (*Client, error) {
	c, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, fmt.Errorf("inspect: connect to %s: %w (is the app running with GNUI_INSPECT=%s, in a development build?)", path, err, path)
	}
	return &Client{c: c, r: bufio.NewReaderSize(c, 64<<10)}, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.c.Close() }

// Call sends req and returns the response. A response with OK false is not an
// error here: its Error says what failed. wait bounds the wait for the answer.
func (c *Client) Call(req *Request, wait time.Duration) (*Response, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.c.SetDeadline(time.Now().Add(wait))
	if _, err := c.c.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("inspect: send: %w", err)
	}
	line, err := ReadLine(c.r, 1<<30)
	if err != nil {
		return nil, fmt.Errorf("inspect: no response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("inspect: bad response: %w", err)
	}
	return &resp, nil
}
