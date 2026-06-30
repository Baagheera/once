package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	once "github.com/Baagheera/once/internal/once"
)

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

func TestRejectsInvalidKey(t *testing.T) {
	handler := newTestHandler(t)

	res := request(t, handler, "POST", "/v1/reserve", `{"key":"bad/key"}`)
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
