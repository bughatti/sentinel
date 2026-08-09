package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// notificationProxyRequest is the request body for the HA push proxy endpoint.
type notificationProxyRequest struct {
	// PushURL is the Home Assistant mobile app webhook URL to forward to.
	PushURL string `json:"push_url"`
	// Payload is the raw notification payload (passed through verbatim).
	Payload json.RawMessage `json:"payload"`
}

// proxyHTTPClient is used for forwarding notification requests.
var proxyHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// handleNotificationProxy — POST /api/notifications/proxy
// Accepts a JSON body with {"push_url": "...", "payload": {...}} and forwards
// the payload to the HA mobile app push URL. This avoids CORS issues in the
// browser UI by routing pushes through the server.
func (s *Server) handleNotificationProxy(w http.ResponseWriter, r *http.Request) {
	var req notificationProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	defer r.Body.Close()

	if req.PushURL == "" {
		writeError(w, http.StatusBadRequest, "push_url is required")
		return
	}
	if len(req.Payload) == 0 {
		writeError(w, http.StatusBadRequest, "payload is required")
		return
	}

	resp, err := proxyHTTPClient.Post(req.PushURL, "application/json", bytes.NewReader(req.Payload))
	if err != nil {
		slog.Warn("notification proxy: forward failed", "url", req.PushURL, "err", err)
		writeError(w, http.StatusBadGateway, "forward failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	slog.Debug("notification proxy: forwarded",
		"url", req.PushURL,
		"status", resp.StatusCode,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if len(body) > 0 {
		fmt.Fprint(w, string(body))
	} else {
		fmt.Fprintf(w, `{"status":%d}`, resp.StatusCode)
	}
}
