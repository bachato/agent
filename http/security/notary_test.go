package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/portainer/portainer/pkg/fips"

	"github.com/stretchr/testify/require"
)

func TestNotaryService(t *testing.T) {
	fips.InitFIPS(false)

	// Signature verification on
	s := NewNotaryService(nil, true)
	require.NotNil(t, s)
	require.True(t, s.signatureVerification)

	// Signature verification off
	s = NewNotaryService(nil, false)
	require.NotNil(t, s)
	require.False(t, s.signatureVerification)
}

func TestNotaryServiceFIPS(t *testing.T) {
	expectedStatusCode := http.StatusTeapot

	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(expectedStatusCode)
	})

	// Signature verification on (must have no effect in FIPS mode)
	s := newNotaryService(nil, true, true)
	require.NotNil(t, s)
	require.False(t, s.signatureVerification)

	newHandler := s.DigitalSignatureVerification(handler)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)

	newHandler.ServeHTTP(rr, req)
	require.Equal(t, expectedStatusCode, rr.Code)

	// Signature verification off
	s = newNotaryService(nil, false, true)
	require.NotNil(t, s)
	require.False(t, s.signatureVerification)

	rr = httptest.NewRecorder()
	req, err = http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)

	newHandler.ServeHTTP(rr, req)
	require.Equal(t, expectedStatusCode, rr.Code)
}
