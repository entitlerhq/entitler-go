package entitler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// fakeAnswer is one answer of the fake transport, or a failure.
type fakeAnswer struct {
	status  int
	header  map[string]string
	body    string
	failure string
}

// seen is a request the fake transport received.
type seen struct {
	method string
	url    string
	header http.Header
	body   string
	start  time.Time
	end    time.Time
}

// fakeHTTP answers from a queue, then from handle, then 500; it records every request.
type fakeHTTP struct {
	mu       sync.Mutex
	answers  []fakeAnswer
	handle   func(r *http.Request, body string) fakeAnswer
	requests []*seen
}

type cutOffBody struct{ r io.Reader }

func (b *cutOffBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF {
		return n, errors.New("connection reset by peer")
	}
	return n, err
}

func (b *cutOffBody) Close() error { return nil }

func (f *fakeHTTP) RoundTrip(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		body = string(data)
	}
	s := &seen{method: req.Method, url: req.URL.String(), header: req.Header.Clone(), body: body, start: time.Now()}
	f.mu.Lock()
	f.requests = append(f.requests, s)
	var a fakeAnswer
	switch {
	case len(f.answers) > 0:
		a, f.answers = f.answers[0], f.answers[1:]
	case f.handle != nil:
		handle := f.handle
		f.mu.Unlock()
		a = handle(req, body)
		f.mu.Lock()
	default:
		a = fakeAnswer{status: 500}
	}
	f.mu.Unlock()
	defer func() { s.end = time.Now() }()
	switch a.failure {
	case "connection":
		return nil, errors.New("connection refused")
	case "timeout", "hang":
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	h := http.Header{}
	for k, v := range a.header {
		h.Set(k, v)
	}
	resp := &http.Response{StatusCode: a.status, Header: h, Request: req,
		Body: io.NopCloser(strings.NewReader(a.body))}
	if a.failure == "cut-off" {
		h.Set("Content-Type", "application/json")
		resp.StatusCode = 200
		resp.Body = &cutOffBody{r: strings.NewReader(`{"customer":"user_42","asOf":`)}
	}
	return resp, nil
}

func (f *fakeHTTP) seen() []*seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*seen(nil), f.requests...)
}

func (f *fakeHTTP) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeHTTP) push(answers ...fakeAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, answers...)
}

func (f *fakeHTTP) client() *http.Client { return &http.Client{Transport: f} }

// clock sets an engine's clock to start at the given instant and follow the (fake) time since.
func clock(e *engine, at time.Time) {
	started := time.Now()
	e.now = func() time.Time { return at.Add(time.Since(started)) }
}

func instant(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := parseInstant(s)
	if err != nil {
		t.Fatalf("instant %q: %v", s, err)
	}
	return at
}

const testKey = "ent_test_conformance_server_key"

func testServer(t *testing.T, f *fakeHTTP, opts ...ServerOption) *Server {
	t.Helper()
	opts = append([]ServerOption{WithHTTPClient(f.client()), WithMaxRetries(0)}, opts...)
	srv, err := NewServer(testKey, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// goMessage turns a message the specification gives into Go's form: a lower-case first letter
// (except Entitler) and no final full stop, with identifiers in Go's spelling.
func goMessage(spec string) string {
	m := strings.TrimSuffix(spec, ".")
	m = strings.NewReplacer(
		"EntitlerClient", "entitler.Client",
		"EntitlerServer", "entitler.Server",
		"snapshot()", "Snapshot()",
		"snapshotKeys()", "SnapshotKeys()",
	).Replace(m)
	if strings.HasPrefix(m, "Entitler") {
		return m
	}
	r, size := utf8.DecodeRuneInString(m)
	return string(unicode.ToLower(r)) + m[size:]
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		t.Fatal(err)
	}
}

func wantArgError(t *testing.T, err error, message string) {
	t.Helper()
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want an argument error %q, got %v", message, err)
	}
	if err.Error() != goMessage(message) {
		t.Fatalf("message:\n got %q\nwant %q", err.Error(), goMessage(message))
	}
}

var ctx = context.Background()

func checkBody(customer, feature string) string {
	return `{"customer":"` + customer + `","asOf":"2026-07-01T09:30:00.000Z","feature":"` + feature +
		`","type":"boolean","entitled":true,"value":true,"sources":[],"upgrades":[],` +
		`"environment":{"id":"env_1","name":"Development","kind":"test"},"track":{"id":"trk_1","name":"All customers"},` +
		`"release":2,"change":null,"testers":false,"experiment":null}`
}

// tokenFor writes an unsigned JWT-shaped token that expires at exp, issued an hour before it.
func tokenFor(exp time.Time) string {
	return tokenWith(exp.Add(-time.Hour), exp, "sig")
}

func tokenWith(iat, exp time.Time, signature string) string {
	enc := base64.RawURLEncoding
	payload := `{"sub":"user_42","iat":` + strconv.FormatInt(iat.Unix(), 10) + `,"exp":` + strconv.FormatInt(exp.Unix(), 10) + `}`
	return enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString([]byte(payload)) + "." + signature
}
