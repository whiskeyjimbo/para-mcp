package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_ValidHeader_Passes(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(requestIDHeader, "req_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should be called for a valid request ID")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequestIDMiddleware_MalformedHeader_InvalidArgument(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler must not be called for a malformed request ID")
	})
	h := RequestIDMiddleware(inner)

	cases := []string{
		"not-a-request-id",
		"req_",
		"req_toolong123456789012345678",
		"REQ_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"req_01ARZ3NDEKTSV4RRFFQ69G5FA!", // invalid character
	}
	for _, id := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(requestIDHeader, id)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("id=%q: expected 400, got %d", id, rr.Code)
		}
		body := rr.Body.String()
		if len(body) < len("invalid_argument") {
			t.Errorf("id=%q: response body missing invalid_argument prefix: %q", id, body)
		}
	}
}

func TestRequestIDMiddleware_NoHeader_Passes(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should be called when header is absent")
	}
}
