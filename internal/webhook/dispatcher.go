package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
)

// ConnectionInfo holds lightweight connection metadata used in CloudEvent headers.
type ConnectionInfo struct {
	ID     string
	UserID string
}

// ConnectRequest is the payload sent to the upstream on sys.connect events.
type ConnectRequest struct {
	Claims             map[string]interface{} `json:"claims"`
	Query              map[string][]string    `json:"query"`
	Headers            map[string][]string    `json:"headers"`
	ClientCertificates []interface{}          `json:"clientCertificates"`
}

// ConnectResponse is the upstream's optional response to a sys.connect event.
type ConnectResponse struct {
	UserID      string   `json:"userId,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Subprotocol string   `json:"subprotocol,omitempty"`
}

// MessageResponse is the upstream's optional response to a user.message event.
type MessageResponse struct {
	Data        []byte
	ContentType string
}

// Dispatcher sends CloudEvents to upstream webhook handlers.
type Dispatcher struct {
	upstreams map[string]string // hubName → URL
	client    *http.Client
	logger    *zap.Logger
}

// New creates a Dispatcher from the hub configuration.
func New(hubs []config.HubConfig, logger *zap.Logger) *Dispatcher {
	upstreams := make(map[string]string, len(hubs))
	for _, h := range hubs {
		if h.EventHandlerURL != "" {
			upstreams[h.Name] = h.EventHandlerURL
		}
	}
	return &Dispatcher{
		upstreams: upstreams,
		client:    &http.Client{Timeout: 10 * time.Second},
		logger:    logger,
	}
}

// HasUpstream returns true if the hub has an upstream webhook configured.
func (d *Dispatcher) HasUpstream(hubName string) bool {
	_, ok := d.upstreams[hubName]
	return ok
}

// ValidateUpstreams performs the CloudEvents abuse protection OPTIONS handshake
// for all configured upstream URLs.
func (d *Dispatcher) ValidateUpstreams(ctx context.Context) {
	for hubName, url := range d.upstreams {
		if err := d.validateUpstream(ctx, url); err != nil {
			d.logger.Warn("abuse protection validation failed",
				zap.String("hub", hubName),
				zap.String("url", url),
				zap.Error(err),
			)
		} else {
			d.logger.Info("upstream validated",
				zap.String("hub", hubName),
				zap.String("url", url),
			)
		}
	}
}

func (d *Dispatcher) validateUpstream(ctx context.Context, upstreamURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, upstreamURL, nil)
	if err != nil {
		return fmt.Errorf("creating OPTIONS request: %w", err)
	}
	req.Header.Set("WebHook-Request-Origin", "localhost")
	req.Header.Set("WebHook-Request-Rate", "0")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("OPTIONS request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upstream returned %d for OPTIONS", resp.StatusCode)
	}
	return nil
}

// SendConnect fires a sys.connect event synchronously before the WebSocket upgrade.
// Returns nil, nil when no upstream is configured (connection is allowed by default).
// A non-nil error means the upstream rejected the connection.
func (d *Dispatcher) SendConnect(ctx context.Context, host, hubName string, conn ConnectionInfo, req ConnectRequest) (*ConnectResponse, error) {
	url, ok := d.upstreams[hubName]
	if !ok {
		return nil, nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling connect request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating connect request: %w", err)
	}
	setCEHeaders(httpReq, host, hubName, conn, "azure.webpubsub.sys.connect")
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending connect event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		data, _ := io.ReadAll(resp.Body)
		if len(data) == 0 {
			return &ConnectResponse{}, nil
		}
		var cr ConnectResponse
		if err := json.Unmarshal(data, &cr); err != nil {
			return &ConnectResponse{}, nil
		}
		return &cr, nil
	}

	return nil, fmt.Errorf("upstream rejected connection with status %d", resp.StatusCode)
}

// SendConnected fires a sys.connected event asynchronously (fire-and-forget).
func (d *Dispatcher) SendConnected(host, hubName string, conn ConnectionInfo) {
	url, ok := d.upstreams[hubName]
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
		if err != nil {
			d.logger.Warn("creating connected request", zap.Error(err))
			return
		}
		setCEHeaders(httpReq, host, hubName, conn, "azure.webpubsub.sys.connected")
		httpReq.Header.Set("Content-Length", "0")

		resp, err := d.client.Do(httpReq)
		if err != nil {
			d.logger.Warn("sending connected event",
				zap.String("hub", hubName),
				zap.String("connectionId", conn.ID),
				zap.Error(err),
			)
			return
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()
}

// SendDisconnected fires a sys.disconnected event asynchronously (fire-and-forget).
func (d *Dispatcher) SendDisconnected(host, hubName string, conn ConnectionInfo, reason string) {
	url, ok := d.upstreams[hubName]
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		body, _ := json.Marshal(map[string]string{"reason": reason})
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			d.logger.Warn("creating disconnected request", zap.Error(err))
			return
		}
		setCEHeaders(httpReq, host, hubName, conn, "azure.webpubsub.sys.disconnected")
		httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

		resp, err := d.client.Do(httpReq)
		if err != nil {
			d.logger.Warn("sending disconnected event",
				zap.String("hub", hubName),
				zap.String("connectionId", conn.ID),
				zap.Error(err),
			)
			return
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()
}

// SendMessage fires a user.message event synchronously.
// Returns nil, nil when no upstream is configured.
func (d *Dispatcher) SendMessage(ctx context.Context, host, hubName string, conn ConnectionInfo, msgType int, data []byte) (*MessageResponse, error) {
	url, ok := d.upstreams[hubName]
	if !ok {
		return nil, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating message request: %w", err)
	}
	setCEHeaders(httpReq, host, hubName, conn, "azure.webpubsub.user.message")
	if msgType == 2 { // binary
		httpReq.Header.Set("Content-Type", "application/octet-stream")
	} else {
		httpReq.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending message event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, nil
	}

	if resp.StatusCode == http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		ct := resp.Header.Get("Content-Type")
		return &MessageResponse{Data: respData, ContentType: ct}, nil
	}

	return nil, fmt.Errorf("upstream returned %d for message", resp.StatusCode)
}

// setCEHeaders adds required CloudEvents HTTP binding headers to the request.
func setCEHeaders(req *http.Request, host, hubName string, conn ConnectionInfo, ceType string) {
	req.Header.Set("ce-specversion", "1.0")
	req.Header.Set("ce-id", uuid.New().String())
	req.Header.Set("ce-type", ceType)
	req.Header.Set("ce-source", fmt.Sprintf("//%s/hubs/%s", host, hubName))
	req.Header.Set("ce-time", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("ce-connectionid", conn.ID)
	req.Header.Set("ce-hub", hubName)
	if conn.UserID != "" {
		req.Header.Set("ce-userid", conn.UserID)
	}
}
