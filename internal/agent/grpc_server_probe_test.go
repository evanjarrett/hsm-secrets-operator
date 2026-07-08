/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agent

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/hsm"
)

// blockingHSMClient embeds a real MockClient (satisfying every Client method) but
// overrides GetInfo to block until released. It stands in for a wedged PKCS#11 call
// that is parked holding the client's lock and ignores context cancellation, so the
// probe cannot rely on GetInfo honoring its deadline.
type blockingHSMClient struct {
	*hsm.MockClient
	release chan struct{}
	entered chan struct{}
}

func newBlockingHSMClient(t *testing.T) *blockingHSMClient {
	t.Helper()
	mock := hsm.NewMockClient()
	require.NoError(t, mock.Initialize(context.Background(), hsm.DefaultConfig()))
	return &blockingHSMClient{
		MockClient: mock,
		release:    make(chan struct{}),
		entered:    make(chan struct{}, 1),
	}
}

// GetInfo blocks until the test releases it, deliberately ignoring ctx to model a
// CGO/libusb call wedged behind the client lock.
func (b *blockingHSMClient) GetInfo(_ context.Context) (*hsm.HSMInfo, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return &hsm.HSMInfo{Label: "unblocked"}, nil
}

// TestProbeTokenTimesOutWhenGetInfoWedged verifies probeToken enforces
// healthProbeTimeout even when GetInfo never returns (a wedged device holding the
// PKCS#11 lock), rather than parking the caller until the call unblocks.
func TestProbeTokenTimesOutWhenGetInfoWedged(t *testing.T) {
	client := newBlockingHSMClient(t)
	// Let the parked goroutine drain and exit once the test finishes.
	t.Cleanup(func() { close(client.release) })

	server := NewGRPCServer(client, 9090, 8080, logr.Discard())

	start := time.Now()
	err := server.probeToken(context.Background())
	elapsed := time.Since(start)

	require.Error(t, err, "probe must fail when the token call is wedged")
	<-client.entered // sanity: GetInfo was actually called and is still blocked
	assert.GreaterOrEqual(t, elapsed, healthProbeTimeout, "probe returned before its deadline")
	assert.Less(t, elapsed, healthProbeTimeout+2*time.Second, "probe did not honor the timeout")
}

// TestHealthHandlersReturnWhenGetInfoWedged verifies the HTTP probe handlers respond
// 503 within a bounded time instead of hanging when GetInfo is wedged.
func TestHealthHandlersReturnWhenGetInfoWedged(t *testing.T) {
	client := newBlockingHSMClient(t)
	t.Cleanup(func() { close(client.release) })

	server := NewGRPCServer(client, 9090, 8080, logr.Discard())

	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"readyz", server.handleReadyz},
		{"healthz", server.handleHealthz},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan int, 1)
			go func() { done <- probeStatus(tc.handler) }()

			select {
			case code := <-done:
				assert.Equal(t, http.StatusServiceUnavailable, code)
			case <-time.After(healthProbeTimeout + 3*time.Second):
				t.Fatal("health handler hung on a wedged GetInfo instead of timing out")
			}
		})
	}
}
