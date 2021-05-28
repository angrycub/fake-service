package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/nicholasjackson/fake-service/logging"
	"github.com/stretchr/testify/assert"
)

func setupReady(t *testing.T, code int, delay time.Duration) *Ready {
	return NewReady(
		logging.NewLogger(&logging.NullMetrics{}, hclog.Default(), nil),
		code,
		delay,
	)
}

func TestReadyReturnsCorrectResponseWhenNoDelay(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h := setupReady(t, http.StatusOK, 0)

	h.Handle(rr, r)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, OKMessage, rr.Body.String())
}

func TestReadyReturnsUnavailableResponseWhenDelayNotElapsed(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h := setupReady(t, http.StatusOK, 10*time.Millisecond)

	h.Handle(rr, r)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, StartingMessage, rr.Body.String())
}

func TestReadyReturnsOKResponseWhenDelayElapsed(t *testing.T) {
	h := setupReady(t, http.StatusOK, 10*time.Millisecond)

	calls := 0

	assert.Eventually(
		t,
		func() bool {

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			rr := httptest.NewRecorder()
			h.Handle(rr, r)
			calls++
			return rr.Code == http.StatusOK && rr.Body.String() == OKMessage
		},
		100*time.Millisecond,
		1*time.Millisecond,
	)

	// should be more than 1 call, as there should be at least one unavailable response
	// this test is not coded to a fixed amount due to varing speeds on CI
	assert.Greater(t, calls, 1)
}
