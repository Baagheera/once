package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	once "github.com/Baagheera/once/internal/once"
)

type fakeStore struct {
	err   error
	calls int
}

func (s *fakeStore) Close() error {
	return nil
}

func (s *fakeStore) Reserve(string, []string) (once.Record, bool, error) {
	s.calls++
	return once.Record{}, false, s.err
}

func (s *fakeStore) Commit(string, string, once.State, int, []byte, []byte, string) (once.Record, error) {
	s.calls++
	return once.Record{}, s.err
}

func (s *fakeStore) Get(string) (once.Record, error) {
	s.calls++
	return once.Record{}, s.err
}

func (s *fakeStore) Forget(string, bool, string) (bool, error) {
	s.calls++
	return false, s.err
}

func TestStoreErrorsReturnInternalError(t *testing.T) {
	attempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		attempt string
	}{
		{
			name:   "reserve",
			method: http.MethodPost,
			path:   "/v1/reserve",
			body:   `{"key":"demo"}`,
		},
		{
			name:   "commit",
			method: http.MethodPost,
			path:   "/v1/commit",
			body:   `{"key":"demo","attempt_token":"` + attempt + `","state":"succeeded","exit_code":0}`,
		},
		{
			name:   "get",
			method: http.MethodGet,
			path:   "/v1/records/demo",
		},
		{
			name:    "delete",
			method:  http.MethodDelete,
			path:    "/v1/records/demo",
			attempt: attempt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{err: errors.New("sentinel store error")}
			handler := NewHandler(store, Options{AuthToken: "test-token"})

			var res *httptest.ResponseRecorder
			if tt.attempt == "" {
				res = request(t, handler, tt.method, tt.path, tt.body)
			} else {
				res = requestWithAttempt(t, handler, tt.method, tt.path, tt.body, tt.attempt)
			}

			if res.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			if got := strings.TrimSpace(res.Body.String()); got != `{"error":"internal error"}` {
				t.Fatalf("body = %s", res.Body.String())
			}
			if store.calls != 1 {
				t.Fatalf("store calls = %d, want 1", store.calls)
			}
		})
	}
}

func TestRejectsInvalidStoreArgumentsBeforeCallingStore(t *testing.T) {
	attempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		attempt string
	}{
		{
			name:   "malformed commit attempt token",
			method: http.MethodPost,
			path:   "/v1/commit",
			body:   `{"key":"demo","attempt_token":"%","state":"succeeded","exit_code":0}`,
		},
		{
			name:   "non-terminal commit state",
			method: http.MethodPost,
			path:   "/v1/commit",
			body:   `{"key":"demo","attempt_token":"` + attempt + `","state":"running","exit_code":0}`,
		},
		{
			name:    "malformed delete attempt token",
			method:  http.MethodDelete,
			path:    "/v1/records/demo",
			attempt: "%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{err: errors.New("store should not be called")}
			handler := NewHandler(store, Options{AuthToken: "test-token"})

			var res *httptest.ResponseRecorder
			if tt.attempt == "" {
				res = request(t, handler, tt.method, tt.path, tt.body)
			} else {
				res = requestWithAttempt(t, handler, tt.method, tt.path, tt.body, tt.attempt)
			}

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
		})
	}
}

func TestReserveCommitAndGet(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	if !jsonBool(t, res.Body.Bytes(), "fresh") {
		t.Fatal("first reserve should be fresh")
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	if attempt == "" {
		t.Fatal("missing attempt token")
	}

	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", res.Code, res.Body.String())
	}

	res = request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("second reserve status = %d body = %s", res.Code, res.Body.String())
	}
	if jsonBool(t, res.Body.Bytes(), "fresh") {
		t.Fatal("second reserve should replay existing record")
	}

	res = request(t, handler, "GET", "/v1/records/demo", "")
	if res.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", res.Code, res.Body.String())
	}
	var rec struct {
		State     once.State `json:"state"`
		StdoutB64 []byte     `json:"stdout_b64"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.State != once.Succeeded {
		t.Fatalf("state = %s", rec.State)
	}
	if string(rec.StdoutB64) != "ok\n" {
		t.Fatalf("stdout = %q base64=%s", rec.StdoutB64, base64.StdEncoding.EncodeToString(rec.StdoutB64))
	}
}

func TestReserveRejectsDifferentCommand(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	res = request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","again"]}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDuplicateCommitIsIdempotent(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	body := `{"key":"demo","attempt_token":"` + attempt + `","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}`
	res = request(t, handler, "POST", "/v1/commit", body)
	if res.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", res.Code, res.Body.String())
	}
	res = request(t, handler, "POST", "/v1/commit", body)
	if res.Code != http.StatusOK {
		t.Fatalf("duplicate commit status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDuplicateCommitConflict(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", res.Code, res.Body.String())
	}
	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0,"stdout_b64":"bm8K"}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("duplicate commit status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCommitWithoutReserveReturnsNotFound(t *testing.T) {
	handler := newTestHandler(t)

	attempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	res := request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0}`)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCommitWrongAttemptKeepsRecordRunning(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	wrongAttempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	if wrongAttempt == attempt {
		t.Fatal("unexpected token collision")
	}

	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+wrongAttempt+`","state":"succeeded","exit_code":0}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("wrong-token commit status = %d body = %s", res.Code, res.Body.String())
	}

	res = request(t, handler, "GET", "/v1/records/demo", "")
	if res.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", res.Code, res.Body.String())
	}
	var rec struct {
		State once.State `json:"state"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.State != once.Running {
		t.Fatalf("state = %s, want %s", rec.State, once.Running)
	}

	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0}`)
	if res.Code != http.StatusOK {
		t.Fatalf("correct-token commit status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDeleteRunningNeedsForce(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	res = request(t, handler, "DELETE", "/v1/records/demo", "")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("delete without attempt status = %d body = %s", res.Code, res.Body.String())
	}
	res = requestWithAttempt(t, handler, "DELETE", "/v1/records/demo", "", attempt)
	if res.Code != http.StatusConflict {
		t.Fatalf("delete status = %d body = %s", res.Code, res.Body.String())
	}
	res = requestWithAttempt(t, handler, "DELETE", "/v1/records/demo?force=1", "", attempt)
	if res.Code != http.StatusNoContent {
		t.Fatalf("force delete status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestUnauthorizedRequestsAreRejected(t *testing.T) {
	store, err := once.OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := NewHandler(store, Options{AuthToken: "secret"})

	req := httptest.NewRequest("GET", "/v1/records/demo", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestHealthDoesNotRequireAuth(t *testing.T) {
	store, err := once.OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := NewHandler(store, Options{AuthToken: "secret"})

	req := httptest.NewRequest("GET", "/healthz", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestWrongBearerTokenIsRejected(t *testing.T) {
	store, err := once.OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := NewHandler(store, Options{AuthToken: "secret"})

	req := httptest.NewRequest("GET", "/v1/records/demo", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestRejectsMissingContentType(t *testing.T) {
	handler := newTestHandler(t)

	req := httptest.NewRequest("POST", "/v1/reserve", strings.NewReader(`{"key":"demo"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestRejectsTrailingJSON(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo"}{"key":"other"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestRejectsInvalidJSONGenerically(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":123}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"invalid json"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "cannot unmarshal") {
		t.Fatalf("body leaked decoder details: %s", res.Body.String())
	}
}

func TestRejectsUnknownJSONFields(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","extra":true}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"invalid json"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "unknown field") {
		t.Fatalf("body leaked decoder details: %s", res.Body.String())
	}
}

func TestRejectsOversizedJSONBody(t *testing.T) {
	handler := newTestHandler(t)

	body := `{"key":"` + strings.Repeat("a", 1<<20) + `"}`
	res := request(t, handler, "POST", "/v1/reserve", body)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"invalid json"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
}

func TestRejectsMalformedBase64Output(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"not-secret-enough","state":"succeeded","exit_code":0,"stdout_b64":"%"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"invalid json"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
}

func TestRejectsInvalidKey(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"bad/key"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestRejectsKeyWithSurroundingWhitespace(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":" demo "}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	handler := newTestHandler(t)

	attempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	res := requestWithAttempt(t, handler, "DELETE", "/v1/records/missing", "", attempt)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDeleteFinishedRequiresAttempt(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0}`)
	if res.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", res.Code, res.Body.String())
	}

	res = request(t, handler, "DELETE", "/v1/records/demo", "")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("delete without attempt status = %d body = %s", res.Code, res.Body.String())
	}
	res = requestWithAttempt(t, handler, "DELETE", "/v1/records/demo", "", attempt)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete with attempt status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestDeleteWithWrongAttemptReturnsNotFound(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"demo","command":["send","email"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reserve status = %d body = %s", res.Code, res.Body.String())
	}
	attempt := jsonString(t, res.Body.Bytes(), "attempt_token")
	res = request(t, handler, "POST", "/v1/commit", `{"key":"demo","attempt_token":"`+attempt+`","state":"succeeded","exit_code":0}`)
	if res.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", res.Code, res.Body.String())
	}

	wrongAttempt, err := once.NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	res = requestWithAttempt(t, handler, "DELETE", "/v1/records/demo", "", wrongAttempt)
	if res.Code != http.StatusNotFound {
		t.Fatalf("delete with wrong attempt status = %d body = %s", res.Code, res.Body.String())
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	store, err := once.OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return NewHandler(store, Options{AuthToken: "test-token"})
}

func request(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer test-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func requestWithAttempt(t *testing.T, handler http.Handler, method, path, body, attempt string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Once-Attempt-Token", attempt)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func jsonBool(t *testing.T, data []byte, field string) bool {
	t.Helper()

	var doc map[string]any
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	value, ok := doc[field].(bool)
	if !ok {
		t.Fatalf("%q is not a bool in %s", field, string(data))
	}
	return value
}

func jsonString(t *testing.T, data []byte, field string) string {
	t.Helper()

	var doc map[string]any
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	value, ok := doc[field].(string)
	if !ok {
		t.Fatalf("%q is not a string in %s", field, string(data))
	}
	return value
}
