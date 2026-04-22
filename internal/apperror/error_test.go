package apperror_test

import (
	"errors"
	"testing"

	"github.com/aap/config-server/internal/apperror"
)

func TestError_CodeAndMessage(t *testing.T) {
	err := apperror.New(apperror.CodeNotFound, "service not found")
	if err.Code != apperror.CodeNotFound {
		t.Errorf("expected code %q, got %q", apperror.CodeNotFound, err.Code)
	}
	if err.Error() != "service not found" {
		t.Errorf("unexpected message: %q", err.Error())
	}
}

func TestError_Unwrap(t *testing.T) {
	cause := errors.New("underlying cause")
	err := apperror.Wrap(apperror.CodeInternal, "something failed", cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is should unwrap to cause")
	}
}

func TestError_ErrorsAs(t *testing.T) {
	err := apperror.New(apperror.CodeValidation, "bad input")
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatal("errors.As should match *apperror.Error")
	}
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("unexpected code: %q", appErr.Code)
	}
}

func TestError_Nil_Unwrap(t *testing.T) {
	err := apperror.New(apperror.CodeNotFound, "not found")
	if err.Unwrap() != nil {
		t.Error("Unwrap should return nil when no cause")
	}
}
