package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Client struct {
	BaseURL string
	http    *http.Client
}

const (
	defaultUserAgent  = "compair-cli"
	clientHeaderName  = "X-Compair-Client"
	clientHeaderValue = "cli"
)

func NewClient(base string) *Client {
	if base == "" {
		base = "http://localhost:4000"
	}
	return &Client{
		BaseURL: base,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.BaseURL+path, nil)
	applyDefaultHeaders(req)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		logHTTP(http.MethodGet, path, 0, time.Since(start), "", err)
		return err
	}
	defer resp.Body.Close()
	logHTTP(http.MethodGet, path, resp.StatusCode, time.Since(start), requestIDFromHeaders(resp.Header), nil)
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: %s", path, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) post(path string, payload any, out any) error {
	buf, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	applyDefaultHeaders(req)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		logHTTP(http.MethodPost, path, 0, time.Since(start), "", err)
		return err
	}
	defer resp.Body.Close()
	logHTTP(http.MethodPost, path, resp.StatusCode, time.Since(start), requestIDFromHeaders(resp.Header), nil)
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: %s", path, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) delete(path string) error {
	req, _ := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	applyDefaultHeaders(req)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		logHTTP(http.MethodDelete, path, 0, time.Since(start), "", err)
		return err
	}
	defer resp.Body.Close()
	logHTTP(http.MethodDelete, path, resp.StatusCode, time.Since(start), requestIDFromHeaders(resp.Header), nil)
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE %s: %s", path, string(b))
	}
	return nil
}

type SessionResponse struct {
	ID                 string `json:"id"`
	UserID             string `json:"user_id"`
	Username           string `json:"username,omitempty"`
	DatetimeCreated    string `json:"datetime_created,omitempty"`
	DatetimeValidUntil string `json:"datetime_valid_until,omitempty"`
}

type UserInfo struct {
	UserID                        string `json:"user_id"`
	Username                      string `json:"username"`
	Name                          string `json:"name"`
	IncludeOwnDocumentsInFeedback *bool  `json:"include_own_documents_in_feedback,omitempty"`
	PreferredFeedbackLength       string `json:"preferred_feedback_length,omitempty"`
}

func (c *Client) EnsureSession() (SessionResponse, error) {
	var out SessionResponse
	path := "/load_session"
	if tok := loadTokenFromDisk(); tok != "" {
		path += "?auth_token=" + url.QueryEscape(tok)
	}
	if err := c.get(path, &out); err != nil {
		return out, err
	}
	if out.ID == "" {
		return out, fmt.Errorf("load_session returned no session id")
	}
	return out, nil
}

func (c *Client) LoadUserByID(userID string) (UserInfo, error) {
	var out UserInfo
	if userID == "" {
		return out, fmt.Errorf("user id is required")
	}
	path := "/load_user_by_id?user_id=" + url.QueryEscape(userID)
	if err := c.get(path, &out); err != nil {
		return out, err
	}
	return out, nil
}

func loadTokenFromDisk() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(h, ".compair", "credentials.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var tmp struct {
		AccessToken string `json:"access_token"`
		AuthToken   string `json:"auth_token"`
	}
	_ = json.Unmarshal(b, &tmp)
	if tmp.AccessToken != "" {
		return tmp.AccessToken
	}
	return tmp.AuthToken
}

func applyDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set(clientHeaderName, clientHeaderValue)
	req.Header.Set("Accept", "application/json")
	if tok := loadTokenFromDisk(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("auth-token", tok)
	}
}

func loadUserIDFromDisk() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(h, ".compair", "credentials.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var tmp struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(b, &tmp)
	return tmp.UserID
}
