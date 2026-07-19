package agentipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/agenttransport"
)

type Client struct {
	http       *http.Client
	token      string
	consumerID string
	protocol   int
}

func NewClient(endpointRaw, token, consumerID string) (*Client, error) {
	endpoint, err := agenttransport.Parse(endpointRaw)
	if err != nil {
		return nil, err
	}
	if endpoint.Scheme == agenttransport.SchemeAuto || endpoint.Scheme == agenttransport.SchemeDisabled {
		return nil, fmt.Errorf("agent IPC client requires a discovered or explicit endpoint, got %s", endpoint.Scheme)
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(consumerID) == "" {
		return nil, fmt.Errorf("agent IPC client requires token and consumer ID")
	}
	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return agenttransport.DialContext(ctx, endpoint)
	}
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:         dialer,
				DisableCompression:  true,
				MaxIdleConns:        2,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: 3 * time.Second,
			},
			Timeout: 10 * time.Second,
		},
		token: token, consumerID: consumerID, protocol: agentproto.ProtocolVersion,
	}, nil
}

func (c *Client) Close() {
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func (c *Client) Handshake(ctx context.Context, agentVersion string, capabilities []string) (agentproto.HandshakeResponse, error) {
	var response agentproto.HandshakeResponse
	err := c.do(ctx, http.MethodPost, "/agent/v1/handshake", agentproto.HandshakeRequest{
		ConsumerID: c.consumerID, AgentVersion: agentVersion, Capabilities: capabilities,
	}, &response)
	return response, err
}

func (c *Client) Health(ctx context.Context) (agentproto.HealthResponse, error) {
	var response agentproto.HealthResponse
	err := c.do(ctx, http.MethodGet, "/agent/v1/health", nil, &response)
	return response, err
}

func (c *Client) Events(ctx context.Context, after uint64, limit int) (agentproto.EventsResponse, error) {
	var response agentproto.EventsResponse
	path := "/agent/v1/events?after=" + strconv.FormatUint(after, 10) + "&limit=" + strconv.Itoa(limit)
	err := c.do(ctx, http.MethodGet, path, nil, &response)
	return response, err
}

func (c *Client) Ack(ctx context.Context, sequence uint64) error {
	var response agentproto.AckResponse
	return c.do(ctx, http.MethodPost, "/agent/v1/events/ack", agentproto.AckRequest{Sequence: sequence}, &response)
}

func (c *Client) NextQuery(ctx context.Context) (agentproto.QueryRequest, bool, error) {
	var query agentproto.QueryRequest
	status, err := c.doStatus(ctx, http.MethodGet, "/agent/v1/queries/next", nil, &query)
	if err != nil {
		return agentproto.QueryRequest{}, false, err
	}
	return query, status == http.StatusOK, nil
}

func (c *Client) QueryResult(ctx context.Context, response agentproto.QueryResponse) error {
	var result map[string]bool
	_, err := c.doStatus(ctx, http.MethodPost, "/agent/v1/queries/result", response, &result)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body any, output any) error {
	_, err := c.doStatus(ctx, method, path, body, output)
	return err
}

func (c *Client) doStatus(ctx context.Context, method, path string, body any, output any) (int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(agentproto.HeaderProtocolVersion, strconv.Itoa(c.protocol))
	request.Header.Set(agentproto.HeaderConsumerID, c.consumerID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var protocolErr agentproto.ErrorResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, maxRequestBody)).Decode(&protocolErr); err == nil && protocolErr.Code != "" {
			return response.StatusCode, fmt.Errorf("agent IPC: %s: %s", protocolErr.Code, protocolErr.Message)
		}
		return response.StatusCode, fmt.Errorf("agent IPC: unexpected HTTP status %s", response.Status)
	}
	if output == nil {
		return response.StatusCode, nil
	}
	if response.StatusCode == http.StatusNoContent {
		return response.StatusCode, nil
	}
	return response.StatusCode, json.NewDecoder(io.LimitReader(response.Body, maxRequestBody)).Decode(output)
}
