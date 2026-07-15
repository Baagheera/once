package oncehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	oncepkg "github.com/Baagheera/once/internal/once"
	"github.com/Baagheera/once/internal/server"
)

func TestClientWorksWithRealServer(t *testing.T) {
	store, err := oncepkg.OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	})

	httpServer := httptest.NewServer(server.NewHandler(store, server.Options{AuthToken: "secret-token"}))
	defer httpServer.Close()

	client, err := New(httpServer.URL, WithBearerToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}

	reserved, err := client.Reserve(context.Background(), ReserveRequest{
		Key:     "webhook:event-123",
		Command: []string{"deliver-webhook", "event-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reserved.Fresh || reserved.AttemptToken == "" || reserved.Record.State != Running {
		t.Fatalf("reserve = %#v", reserved)
	}

	committed, err := client.Commit(context.Background(), CommitRequest{
		Key:          reserved.Record.Key,
		AttemptToken: reserved.AttemptToken,
		State:        Succeeded,
		ExitCode:     0,
		Stdout:       []byte("ok\n"),
		Stderr:       []byte("noise\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed.State != Succeeded || string(committed.Stdout) != "ok\n" || string(committed.Stderr) != "noise\n" {
		t.Fatalf("committed = %#v", committed)
	}

	replayed, err := client.Reserve(context.Background(), ReserveRequest{
		Key:     "webhook:event-123",
		Command: []string{"deliver-webhook", "event-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Fresh || string(replayed.Record.Stdout) != "ok\n" {
		t.Fatalf("replayed = %#v", replayed)
	}

	got, err := client.Get(context.Background(), "webhook:event-123")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != Succeeded || string(got.Stdout) != "ok\n" {
		t.Fatalf("got = %#v", got)
	}

	if err := client.Delete(context.Background(), "webhook:event-123", reserved.AttemptToken, false); err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), "webhook:event-123")
	var missing *Error
	if !errors.As(err, &missing) || missing.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete err = %T %v", err, err)
	}

	wrongToken, err := New(httpServer.URL, WithBearerToken("wrong-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrongToken.Get(context.Background(), "webhook:event-123")
	var unauthorized *Error
	if !errors.As(err, &unauthorized) || unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token err = %T %v", err, err)
	}
}

func TestReserveSendsJSONAndDecodesResponse(t *testing.T) {
	started := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/reserve" {
			t.Fatalf("request = %s %s, want POST /v1/reserve", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q", got)
		}

		var req ReserveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Key != "email:user42:welcome" || strings.Join(req.Command, " ") != "send email" {
			t.Fatalf("ReserveRequest = %#v", req)
		}

		writeTestJSON(t, w, http.StatusOK, ReserveResponse{
			Fresh:        true,
			AttemptToken: "attempt-token",
			Record: Record{
				Key:       req.Key,
				State:     Running,
				Command:   req.Command,
				StartedAt: started,
				UpdatedAt: started,
			},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, WithBearerToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Reserve(context.Background(), ReserveRequest{
		Key:     "email:user42:welcome",
		Command: []string{"send", "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Fresh || resp.AttemptToken != "attempt-token" {
		t.Fatalf("ReserveResponse = %#v", resp)
	}
	if resp.Record.State != Running || resp.Record.StartedAt != started {
		t.Fatalf("record = %#v", resp.Record)
	}
}

func TestCommitEncodesOutputAndDecodesRecord(t *testing.T) {
	finished := time.Date(2026, 7, 6, 10, 1, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/commit" {
			t.Fatalf("request = %s %s, want POST /v1/commit", r.Method, r.URL.Path)
		}

		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if raw["stdout_b64"] != "b2sK" || raw["stderr_b64"] != "bm9pc2UK" {
			t.Fatalf("encoded output = %#v", raw)
		}

		writeTestJSON(t, w, http.StatusOK, Record{
			Key:        "demo",
			State:      Succeeded,
			ExitCode:   0,
			Stdout:     []byte("ok\n"),
			Stderr:     []byte("noise\n"),
			FinishedAt: &finished,
			UpdatedAt:  finished,
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := client.Commit(context.Background(), CommitRequest{
		Key:          "demo",
		AttemptToken: "attempt-token",
		State:        Succeeded,
		ExitCode:     0,
		Stdout:       []byte("ok\n"),
		Stderr:       []byte("noise\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != Succeeded || string(rec.Stdout) != "ok\n" || rec.FinishedAt == nil || *rec.FinishedAt != finished {
		t.Fatalf("record = %#v", rec)
	}
}

func TestGetAndDeleteUseRecordEndpoint(t *testing.T) {
	var sawGet bool
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/records/email:user42:welcome":
			sawGet = true
			writeTestJSON(t, w, http.StatusOK, Record{Key: "email:user42:welcome", State: Succeeded})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/records/email:user42:welcome":
			sawDelete = true
			if r.URL.Query().Get("force") != "1" {
				t.Fatalf("force query = %q", r.URL.RawQuery)
			}
			if got := r.Header.Get("X-Once-Attempt-Token"); got != "attempt-token" {
				t.Fatalf("attempt header = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "email:user42:welcome"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), "email:user42:welcome", "attempt-token", true); err != nil {
		t.Fatal(err)
	}
	if !sawGet || !sawDelete {
		t.Fatalf("sawGet=%v sawDelete=%v", sawGet, sawDelete)
	}
}

func TestSuccessResponseSizeIsLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat(" ", 65)))
	}))
	defer server.Close()

	client, err := New(server.URL, WithMaxResponseBytes(64))
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Reserve(context.Background(), ReserveRequest{Key: "demo"})
	if err == nil || !strings.Contains(err.Error(), "response body exceeds 64 bytes") {
		t.Fatalf("err = %v, want response size error", err)
	}
}

func TestHTTPErrorIncludesStatusAndMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, http.StatusConflict, map[string]string{
			"error": "key already exists with a different command",
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Reserve(context.Background(), ReserveRequest{Key: "demo"})
	var httpErr *Error
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want *Error", err, err)
	}
	if httpErr.StatusCode != http.StatusConflict || httpErr.Message != "key already exists with a different command" {
		t.Fatalf("http error = %#v", httpErr)
	}
}

func TestClientDoesNotFollowCommitRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		writeTestJSON(t, w, http.StatusOK, Record{Key: "demo", State: Succeeded})
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	client, err := New(origin.URL, WithBearerToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Commit(context.Background(), CommitRequest{
		Key:          "demo",
		AttemptToken: "attempt-token",
		State:        Succeeded,
	})
	if targetRequests.Load() != 0 {
		t.Fatalf("redirect target received %d requests, want 0", targetRequests.Load())
	}
	assertHTTPStatus(t, err, http.StatusTemporaryRedirect)
}

func TestClientDoesNotFollowDeleteRedirectWithCustomHTTPClient(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	custom := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
	}
	client, err := New(origin.URL, WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	err = client.Delete(context.Background(), "demo", "attempt-token", true)
	if targetRequests.Load() != 0 {
		t.Fatalf("redirect target received %d requests, want 0", targetRequests.Load())
	}
	assertHTTPStatus(t, err, http.StatusTemporaryRedirect)
}

func TestWithHTTPClientUsesConfiguredTransport(t *testing.T) {
	var requests atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"key":"demo","state":"succeeded"}`)),
			Request:    req,
		}, nil
	})
	client, err := New("http://once.invalid", WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("transport received %d requests, want 1", requests.Load())
	}
}

func TestWithHTTPClientRejectsNil(t *testing.T) {
	client, err := New("http://example.test", WithHTTPClient(nil))
	if err == nil || client != nil {
		t.Fatalf("New returned client=%v err=%v, want error", client, err)
	}
}

func TestWithBearerTokenTrimsFileWhitespaceAndRejectsInvalidTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("Authorization = %q", got)
		}
		writeTestJSON(t, w, http.StatusOK, Record{Key: "demo", State: Succeeded})
	}))
	defer server.Close()

	client, err := New(server.URL, WithBearerToken(" secret-token\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{" \n\t", "secret token"} {
		if client, err := New(server.URL, WithBearerToken(token)); err == nil || client != nil {
			t.Fatalf("WithBearerToken(%q) returned client=%v err=%v, want error", token, client, err)
		}
	}
}

func TestNewRejectsEmptyBaseURL(t *testing.T) {
	client, err := New("")
	if err == nil || client != nil {
		t.Fatalf("New returned client=%v err=%v, want error", client, err)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func assertHTTPStatus(t *testing.T, err error, status int) {
	t.Helper()

	var httpErr *Error
	if !errors.As(err, &httpErr) || httpErr.StatusCode != status {
		t.Fatalf("err = %T %v, want HTTP status %d", err, err, status)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
