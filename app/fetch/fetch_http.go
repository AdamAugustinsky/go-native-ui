//go:build !darwin

package fetch

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

// Windows, Linux and the rest: net/http. Each call has a transport of its own
// without keep-alive, as macOS has a session of its own, so nothing is left
// behind when the call returns.
func get(ctx context.Context, u *url.URL, s *sink, opt Options) (Response, error) {
	tr := &http.Transport{
		DisableKeepAlives: true,
		Proxy:             http.ProxyFromEnvironment,
	}
	if p := opt.Proxy; p != nil {
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
	defer tr.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return Response{}, unwrapURL(err)
	}
	defer resp.Body.Close()

	res := Response{Status: resp.StatusCode, URL: resp.Request.URL.String(), ContentLength: resp.ContentLength}
	if !ok(resp.StatusCode) {
		return res, &StatusError{Code: resp.StatusCode, URL: res.URL}
	}
	if err := s.admit(resp.ContentLength); err != nil {
		return res, err
	}
	if _, err := io.Copy(s, resp.Body); err != nil {
		return res, err
	}
	return res, nil
}

// unwrapURL drops the *url.Error that net/http wraps every client error in:
// Get adds the URL itself.
func unwrapURL(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
