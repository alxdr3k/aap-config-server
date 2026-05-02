package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aap/config-server/internal/apperror"
)

func TestInstrumentHTTPRecoversPanicAndWrites500(t *testing.T) {
	panicHandler := func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}
	wrapped := instrumentHTTP("GET /test/panic", panicHandler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/panic", nil)

	// Must not propagate the panic; defer-recover writes 500.
	wrapped(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic should produce 500, got %d", rec.Code)
	}
}

func TestRespondErrorMapsContextCancellationsAwayFrom500(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantKind string
	}{
		{"canceled_raw", context.Canceled, 499, "client_closed_request"},
		{"deadline_raw", context.DeadlineExceeded, http.StatusGatewayTimeout, "deadline_exceeded"},
		{
			"canceled_wrapped_in_apperror_internal",
			apperror.Wrap(apperror.CodeInternal, "refresh secret cache", context.Canceled),
			499,
			"client_closed_request",
		},
		{
			"deadline_wrapped_in_apperror_internal",
			apperror.Wrap(apperror.CodeInternal, "refresh secret cache", context.DeadlineExceeded),
			http.StatusGatewayTimeout,
			"deadline_exceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondError(rec, tc.err)
			if rec.Code != tc.wantCode {
				t.Fatalf("status: want %d, got %d", tc.wantCode, rec.Code)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error.Code != tc.wantKind {
				t.Fatalf("error.code: want %q, got %q", tc.wantKind, body.Error.Code)
			}
		})
	}
}

func TestStatusRecorderDefaultsTo200WhenHandlerIsSilent(t *testing.T) {
	silent := func(w http.ResponseWriter, r *http.Request) { /* no Write/WriteHeader */ }
	wrapped := instrumentHTTP("GET /test/silent", silent)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/silent", nil)
	wrapped(rec, req)

	// httptest.ResponseRecorder defaults to 200 when WriteHeader isn't called,
	// matching what http.Server emits over the wire.
	if rec.Code != http.StatusOK {
		t.Fatalf("silent handler should net 200, got %d", rec.Code)
	}
}
