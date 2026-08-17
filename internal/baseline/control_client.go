package baseline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumControlResponseBytes = 512_000

// ControlHTTPError contains only the server's frozen safe error metadata.
// Response bodies are deliberately never retained in errors.
type ControlHTTPError struct {
	StatusCode int
	Code       string
	Stage      string
	Retryable  bool
}

func (err *ControlHTTPError) Error() string {
	if err == nil || err.Code == "" {
		return "baseline control request failed"
	}
	return "baseline control request failed: " + err.Code
}

type controlResponse struct {
	StatusCode int
	Body       []byte
	BodyBytes  int64
}

type countingBody struct {
	reader *bytes.Reader
	read   int64
}

func (body *countingBody) Read(value []byte) (int, error) {
	count, err := body.reader.Read(value)
	body.read += int64(count)
	return count, err
}

func (body *countingBody) Close() error { return nil }

// ControlClient is a body-redacting, POST-only client for the frozen baseline
// control plane. It does not use Host or forwarded headers as authority input.
type ControlClient struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

func NewControlClient(baseURL, token string, allowLoopbackHTTP bool) (*ControlClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, uploadError(UploadFailureContract, "unsafe_server_authority")
	}
	if strings.TrimSpace(token) == "" {
		return nil, uploadError(UploadFailureAuthentication, "authentication_required")
	}
	if parsed.Scheme != "https" {
		if parsed.Scheme != "http" || !allowLoopbackHTTP || !isDeclaredLoopbackHost(parsed.Hostname()) {
			return nil, uploadError(UploadFailureContract, "verified_https_required")
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if parsed.Scheme == "http" {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, dialErr := dialer.DialContext(ctx, network, address)
			if dialErr != nil {
				return nil, dialErr
			}
			host, _, splitErr := net.SplitHostPort(connection.RemoteAddr().String())
			if splitErr != nil || !isLoopbackIP(host) {
				_ = connection.Close()
				return nil, errors.New("loopback transport connected to a non-loopback peer")
			}
			return connection, nil
		}
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("baseline control redirects are forbidden")
		},
	}
	return &ControlClient{baseURL: parsed, token: token, http: client}, nil
}

func (client *ControlClient) serverIdentitySHA256() string {
	if client == nil || client.baseURL == nil {
		return ""
	}
	normalized := *client.baseURL
	normalized.Scheme = strings.ToLower(normalized.Scheme)
	normalized.Host = strings.ToLower(normalized.Host)
	normalized.Path = strings.TrimRight(normalized.Path, "/")
	digest := sha256.Sum256([]byte(normalized.String()))
	return fmt.Sprintf("%x", digest[:])
}

func isDeclaredLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return isLoopbackIP(host)
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (client *ControlClient) post(ctx context.Context, endpoint string, body []byte) (controlResponse, error) {
	return client.postProtocol(ctx, endpoint, body, ControlProtocolVersion, ControlProtocolSHA256)
}

// postProtocol performs one strict control-plane POST and authenticates frozen
// error envelopes against the protocol selected by the caller. Keeping the
// protocol pin explicit prevents a v2 request from accepting a v1 downgrade.
func (client *ControlClient) postProtocol(ctx context.Context, endpoint string, body []byte, protocolVersion, protocolSHA256 string) (controlResponse, error) {
	if client == nil || client.baseURL == nil || client.http == nil {
		return controlResponse{}, uploadError(UploadFailureInternal, "control_client_unavailable")
	}
	if !strings.HasPrefix(endpoint, "/") || strings.Contains(endpoint, "?") || strings.Contains(endpoint, "#") {
		return controlResponse{}, uploadError(UploadFailureInternal, "invalid_control_endpoint")
	}
	target := strings.TrimRight(client.baseURL.String(), "/") + endpoint
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return controlResponse{}, uploadError(UploadFailureInternal, "request_construction_failed")
	}
	requestBody := &countingBody{reader: bytes.NewReader(body)}
	request.Body = requestBody
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("auth-token", client.token)
	request.Header.Set("User-Agent", "compair-cli")
	request.Header.Set("X-Compair-Client", "cli")

	response, err := client.http.Do(request)
	if err != nil {
		return controlResponse{BodyBytes: requestBody.read}, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumControlResponseBytes+1)
	responseBody, readErr := io.ReadAll(limited)
	if readErr != nil {
		return controlResponse{StatusCode: response.StatusCode, BodyBytes: requestBody.read}, readErr
	}
	if len(responseBody) > maximumControlResponseBytes {
		return controlResponse{StatusCode: response.StatusCode, BodyBytes: requestBody.read}, uploadError(UploadFailureContract, "response_limit_exceeded")
	}
	result := controlResponse{StatusCode: response.StatusCode, Body: responseBody, BodyBytes: requestBody.read}
	if response.StatusCode >= http.StatusBadRequest {
		return result, decodeControlHTTPErrorForProtocol(response.StatusCode, responseBody, protocolVersion, protocolSHA256)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, &ControlHTTPError{StatusCode: response.StatusCode, Code: "unexpected_http_status"}
	}
	return result, nil
}

func decodeControlHTTPError(status int, body []byte) error {
	return decodeControlHTTPErrorForProtocol(status, body, ControlProtocolVersion, ControlProtocolSHA256)
}

func decodeControlHTTPErrorForProtocol(status int, body []byte, protocolVersion, protocolSHA256 string) error {
	var safe struct {
		ProtocolVersion string  `json:"protocol_version"`
		ProtocolSHA256  string  `json:"protocol_sha256"`
		MessageType     string  `json:"message_type"`
		RequestID       *string `json:"request_id"`
		HTTPStatus      int     `json:"http_status"`
		Stage           string  `json:"stage"`
		Retryable       bool    `json:"retryable"`
		Code            string  `json:"code"`
	}
	if err := decodeStrictResponseJSON(body, &safe); err != nil || safe.MessageType != "error" || !validSafeReasonCode(safe.Code) || !validSafeReasonCode(safe.Stage) {
		return &ControlHTTPError{StatusCode: status, Code: "invalid_error_response"}
	}
	if safe.ProtocolVersion != protocolVersion || safe.ProtocolSHA256 != protocolSHA256 || safe.HTTPStatus != status {
		return &ControlHTTPError{StatusCode: status, Code: "protocol_mismatch"}
	}
	return &ControlHTTPError{StatusCode: status, Code: safe.Code, Stage: safe.Stage, Retryable: safe.Retryable}
}

func decodeStrictResponseJSON(value []byte, output any) error {
	if len(value) == 0 {
		return errors.New("empty JSON response")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func controlPath(format string, values ...any) string {
	return fmt.Sprintf(format, values...)
}
