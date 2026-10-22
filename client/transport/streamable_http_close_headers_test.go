package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteRecorder is a legacy server that gives every initialize a session and
// records the headers of the DELETE that ends it.
type deleteRecorder struct {
	mu      sync.Mutex
	deletes []http.Header
}

func (s *deleteRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set(HeaderKeySessionID, "session-1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
	case http.MethodDelete:
		s.mu.Lock()
		s.deletes = append(s.deletes, r.Header.Clone())
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *deleteRecorder) deleteHeaders(t *testing.T) http.Header {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.deletes, 1, "Close should send one DELETE")
	return s.deletes[0]
}

func startSessionAndClose(t *testing.T, srv *deleteRecorder, opts ...StreamableHTTPCOption) http.Header {
	t.Helper()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	trans, err := NewStreamableHTTP(ts.URL, opts...)
	require.NoError(t, err)
	require.NoError(t, trans.Start(t.Context()))

	_, err = trans.SendRequest(t.Context(), JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcp.NewRequestId(1),
		Method:  "initialize",
	})
	require.NoError(t, err)
	require.Equal(t, "session-1", trans.GetSessionId())

	require.NoError(t, trans.Close())
	return srv.deleteHeaders(t)
}

func TestStreamableHTTP_CloseSendsConfiguredHeaders(t *testing.T) {
	h := startSessionAndClose(t, &deleteRecorder{},
		WithHTTPHeaders(map[string]string{"Authorization": "Bearer static-key", "X-Static": "s"}),
		WithHTTPHeaderFunc(func(context.Context) map[string]string { return map[string]string{"X-Dynamic": "d"} }),
	)

	assert.Equal(t, "session-1", h.Get(HeaderKeySessionID))
	assert.Equal(t, "Bearer static-key", h.Get("Authorization"))
	assert.Equal(t, "s", h.Get("X-Static"))
	assert.Equal(t, "d", h.Get("X-Dynamic"))
}

func TestStreamableHTTP_CloseSendsOAuthAuthorization(t *testing.T) {
	store := NewMemoryTokenStore()
	require.NoError(t, store.SaveToken(t.Context(), &Token{
		AccessToken: "oauth-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}))

	h := startSessionAndClose(t, &deleteRecorder{}, WithHTTPOAuth(OAuthConfig{ClientID: "c", TokenStore: store}))

	assert.Equal(t, "Bearer oauth-token", h.Get("Authorization"))
}
