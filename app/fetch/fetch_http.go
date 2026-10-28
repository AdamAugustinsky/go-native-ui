//go:build !darwin && !windows

package fetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

// Linux and the rest: net/http. Each call has a transport of its own
// without keep-alive, as macOS has a session of its own, so nothing is left
// behind when the call returns.

// transport is a one-call transport with the proxy of opt.
func transport(opt Options) *http.Transport {
	tr := &http.Transport{
		DisableKeepAlives: true,
		Proxy:             http.ProxyFromEnvironment,
	}
	if p := opt.Proxy; p != nil && p.kind() == ProxyNone {
		tr.Proxy = nil
	} else if p != nil {
		pu := &url.URL{Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
		switch p.kind() {
		case ProxyHTTP:
			pu.Scheme = "http"
		case ProxyHTTPS:
			pu.Scheme = "https"
		case ProxySOCKS5:
			pu.Scheme = "socks5"
		}
		if p.User != "" || p.Password != "" {
			pu.User = url.UserPassword(p.User, p.Password)
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	return tr
}

// errEarlyResponse ends the body's pipe when the response arrived first.
var errEarlyResponse = errors.New("fetch: the server answered before the request body was sent")

func do(ctx context.Context, u *url.URL, c *call, s *sink, opt Options) (Response, error) {
	tr := transport(opt)
	defer tr.CloseIdleConnections()

	// The body goes through a pipe that the calling goroutine fills, so
	// that the body is read and UploadProgress called there; the transport
	// reads the other end. A body of known length 0 is no body at all.
	var body io.Reader
	var pr *io.PipeReader
	var pw *io.PipeWriter
	if c.hasBody() && c.length != 0 {
		pr, pw = io.Pipe()
		body = pr
	}
	req, err := http.NewRequestWithContext(ctx, c.method, u.String(), body)
	if err != nil {
		return Response{}, err
	}
	if c.hasBody() {
		req.ContentLength = c.length // -1: chunked
		if c.length == 0 {
			req.Body = http.NoBody
		}
	}
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: tr}

	var resp *http.Response
	if pw == nil {
		resp, err = client.Do(req)
	} else {
		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, err = client.Do(req)
			pr.CloseWithError(errEarlyResponse)
		}()
		bodyErr := pump(c, pw)
		<-done
		if bodyErr != nil {
			if resp != nil {
				resp.Body.Close()
			}
			return Response{}, bodyErr
		}
	}
	if err != nil {
		return Response{}, unwrapURL(err)
	}
	defer resp.Body.Close()

	res := Response{Status: resp.StatusCode, URL: resp.Request.URL.String(), ContentLength: resp.ContentLength}
	if c.headers {
		res.Headers = make(map[string]string, len(resp.Header))
		for k, vs := range resp.Header {
			for _, v := range vs {
				addHeader(res.Headers, k, v)
			}
		}
	}
	if !c.accepts(resp.StatusCode) {
		return res, &StatusError{Code: resp.StatusCode, URL: res.URL}
	}
	if c.method == "HEAD" {
		return res, nil
	}
	if err := s.admit(resp.ContentLength); err != nil {
		return res, err
	}
	if _, err := io.Copy(s, resp.Body); err != nil {
		return res, err
	}
	return res, nil
}

// pump copies the body into pw until it ends, the transport stops reading
// (the pipe's reader closed: the response came, or the request failed), or
// the body fails, which it returns.
func pump(c *call, pw *io.PipeWriter) error {
	buf := make([]byte, 256<<10)
	for {
		n, err := c.fill(buf)
		if err == io.EOF {
			pw.Close()
			return nil
		}
		if err != nil {
			pw.CloseWithError(err)
			return err
		}
		if _, err := pw.Write(buf[:n]); err != nil {
			return nil // the request's own error says why
		}
		c.advance(n)
	}
}

// unwrapURL drops the *url.Error that net/http wraps every client error in:
// Get adds the URL itself.
func unwrapURL(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
