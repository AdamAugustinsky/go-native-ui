package fetch_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gonativeui/app/fetch"
)

// TestDoContract is Do's wave-0 behaviour: a plain GET is Get, other
// requests are refused as unsupported until N1, bad methods and URLs fail.
func TestDoContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello " + r.Method))
	}))
	defer srv.Close()
	ctx := context.Background()

	var buf bytes.Buffer
	res, err := fetch.Do(ctx, fetch.Request{URL: srv.URL}, &buf, fetch.Options{})
	if err != nil || res.Status != 200 || buf.String() != "hello GET" {
		t.Fatalf("GET: %v %+v %q", err, res, buf.String())
	}
	if _, err := fetch.Do(ctx, fetch.Request{URL: srv.URL}, nil, fetch.Options{}); err != nil {
		t.Fatalf("GET into nil: %v", err)
	}
	_, err = fetch.Do(ctx, fetch.Request{Method: "PUT", URL: srv.URL, Body: strings.NewReader("x"), BodyLength: 1}, nil, fetch.Options{})
	if !errors.Is(err, fetch.ErrUnsupportedRequest) || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("PUT: %v", err)
	}
	if _, err := fetch.Do(ctx, fetch.Request{Method: "BAD METHOD", URL: srv.URL}, nil, fetch.Options{}); !errors.Is(err, fetch.ErrBadMethod) {
		t.Fatalf("bad method: %v", err)
	}
	if _, err := fetch.Do(ctx, fetch.Request{URL: "ftp://x"}, nil, fetch.Options{}); !errors.Is(err, fetch.ErrBadURL) {
		t.Fatalf("bad URL: %v", err)
	}
}
