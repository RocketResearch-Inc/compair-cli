package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Login
type LoginRequest struct {
	Email    string `json:"username"`
	Password string `json:"password"`
}
type LoginResponse struct {
	AuthToken    string `json:"auth_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Username     string `json:"username,omitempty"`
}

func (c *Client) Login(email, password string) (LoginResponse, error) {
	var out LoginResponse
	if err := c.post("/login", LoginRequest{email, password}, &out); err != nil {
		return out, err
	}
	return out, nil
}

type GoogleOAuthDeviceStartResponse struct {
	AuthURL   string `json:"auth_url,omitempty"`
	PollToken string `json:"poll_token,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type GoogleOAuthDevicePollResponse struct {
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
	AuthToken     string `json:"auth_token,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	Username      string `json:"username,omitempty"`
	Name          string `json:"name,omitempty"`
	AccountStatus string `json:"account_status,omitempty"`
}

func (c *Client) StartGoogleOAuthDevice() (GoogleOAuthDeviceStartResponse, error) {
	var out GoogleOAuthDeviceStartResponse
	if err := c.post("/auth/google/device/start", map[string]any{"client": "cli"}, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *Client) PollGoogleOAuthDevice(pollToken string) (GoogleOAuthDevicePollResponse, error) {
	var out GoogleOAuthDevicePollResponse
	if strings.TrimSpace(pollToken) == "" {
		return out, fmt.Errorf("poll token is required")
	}
	if err := c.get("/auth/google/device/poll?poll_token="+url.QueryEscape(strings.TrimSpace(pollToken)), &out); err != nil {
		return out, err
	}
	return out, nil
}

type SignUpRequest struct {
	Username     string    `json:"username"`
	Name         string    `json:"name"`
	Password     string    `json:"password"`
	Groups       *[]string `json:"groups"`
	ReferralCode *string   `json:"referral_code"`
}

type SignUpResponse struct {
	Message string `json:"message,omitempty"`
}

func (c *Client) SignUp(email, name, password string, referral string) (SignUpResponse, error) {
	var out SignUpResponse
	var referralPtr *string
	if trimmed := strings.TrimSpace(referral); trimmed != "" {
		referralPtr = new(string)
		*referralPtr = trimmed
	}
	payload := SignUpRequest{
		Username:     email,
		Name:         name,
		Password:     password,
		ReferralCode: referralPtr,
	}
	if err := c.post("/sign-up", payload, &out); err != nil {
		return out, err
	}
	return out, nil
}

// form helpers
func (c *Client) postForm(path string, data url.Values, out any) error {
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+path, bytes.NewBufferString(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

func (c *Client) postMultipart(path string, fields map[string]string, fileField, filePath string, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if fileField != "" && filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		fw, err := mw.CreateFormFile(fileField, filepath.Base(filePath))
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, f); err != nil {
			return err
		}
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
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

// Groups
type GroupItem struct {
	ID         string `json:"id,omitempty"`
	GroupID    string `json:"group_id,omitempty"`
	Name       string `json:"name"`
	Visibility string `json:"visibility,omitempty"`
}
type GroupListResp struct {
	Groups     []GroupItem `json:"groups"`
	TotalCount int         `json:"total_count"`
}

func (c *Client) ListGroups(ownOnly bool) ([]GroupItem, error) {
	const pageSize = 100
	q := url.Values{}
	if ownOnly {
		q.Set("own_groups_only", "true")
	}
	if uid := loadUserIDFromDisk(); uid != "" {
		q.Set("user_id", uid)
	}
	q.Set("page_size", strconv.Itoa(pageSize))

	var all []GroupItem
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		path := "/load_groups"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var out GroupListResp
		if err := c.get(path, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Groups...)
		if len(out.Groups) == 0 || len(out.Groups) < pageSize || (out.TotalCount > 0 && len(all) >= out.TotalCount) {
			break
		}
	}
	return all, nil
}

func (c *Client) CreateGroup(name, category, description, visibility, imagePath string) (GroupItem, error) {
	fields := map[string]string{"name": name}
	if category != "" {
		fields["category"] = category
	}
	if description != "" {
		fields["description"] = description
	}
	if visibility != "" {
		fields["visibility"] = visibility
	}
	out := GroupItem{Name: name, Visibility: visibility}
	err := c.postMultipart("/create_group", fields, "file", imagePath, &out)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		return GroupItem{}, err
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = name
	}
	if strings.TrimSpace(out.Visibility) == "" {
		out.Visibility = visibility
	}
	return out, nil
}

type GroupUser struct {
	ID           string `json:"user_id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Role         string `json:"role"`
	ProfileImage string `json:"profile_image"`
}

func (c *Client) ListGroupUsers(groupID string) ([]GroupUser, error) {
	var out struct {
		Users []GroupUser `json:"users"`
	}
	if err := c.get("/load_group_users?group_id="+url.QueryEscape(groupID), &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (c *Client) DeleteGroup(groupID string) error {
	data := url.Values{"group_id": {groupID}}
	// Try a series of common patterns/endpoints to maximize compatibility
	// 1) Legacy form endpoint
	if err := c.postForm("/delete_group", data, nil); err == nil {
		return nil
	}
	// 2) GET with query param (less ideal, but some backends use it)
	if err := c.get("/delete_group?group_id="+url.QueryEscape(groupID), nil); err == nil {
		return nil
	}
	// 2b) DELETE with query param (common FastAPI style)
	if err := c.delete("/delete_group?group_id=" + url.QueryEscape(groupID)); err == nil {
		return nil
	}
	// 3) RESTful DELETE /groups/{id}
	if err := c.delete("/groups/" + url.PathEscape(groupID)); err == nil {
		return nil
	}
	// 3b) DELETE /delete_group/{id}
	if err := c.delete("/delete_group/" + url.PathEscape(groupID)); err == nil {
		return nil
	}
	// 4) Form POST to /groups/delete
	if err := c.postForm("/groups/delete", data, nil); err == nil {
		return nil
	}
	// 5) JSON POST to /delete_group
	if err := c.post("/delete_group", map[string]string{"group_id": groupID}, nil); err == nil {
		return nil
	}
	// 6) POST to /groups/{id}/delete
	if err := c.post("/groups/"+url.PathEscape(groupID)+"/delete", map[string]string{}, nil); err == nil {
		return nil
	}
	return fmt.Errorf("delete group %s: no compatible endpoint accepted the request (405 or unsupported)", groupID)
}

// Documents: publish/unpublish
func (c *Client) SetDocumentPublished(docID string, published bool) error {
	q := url.Values{}
	q.Set("doc_id", docID)
	if published {
		q.Set("is_published", "true")
	} else {
		q.Set("is_published", "false")
	}
	path := "/publish_doc?" + q.Encode()
	return c.get(path, nil)
}

// Group update
func (c *Client) UpdateGroup(groupID string, attrs map[string]string) error {
	data := url.Values{}
	for k, v := range attrs {
		if v != "" {
			data.Set(k, v)
		}
	}
	// Server expects group_id in query params, others as form fields
	path := "/admin/update_group?group_id=" + url.QueryEscape(groupID)
	return c.postForm(path, data, nil)
}

func (c *Client) UpdateUser(attrs map[string]string) error {
	data := url.Values{}
	for k, v := range attrs {
		if strings.TrimSpace(v) != "" {
			data.Set(k, v)
		}
	}
	return c.postForm("/update_user", data, nil)
}

func (c *Client) LoadUserPlan() (string, error) {
	var out struct {
		Plan string `json:"plan"`
	}
	if err := c.get("/load_user_plan", &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Plan), nil
}

// ListAdminGroupIDs attempts to fetch the set of group IDs where the current user is an admin.
// It supports multiple response shapes for compatibility:
// - {"groups": [{"id":"...","group_id":"..."}, ...]}
// - [{"id":"...","group_id":"..."}, ...]
// - ["group-id-1", "group-id-2", ...]
func (c *Client) ListAdminGroupIDs() ([]string, error) {
	var raw json.RawMessage
	if err := c.get("/admin/groups", &raw); err != nil {
		return nil, err
	}
	if os.Getenv("COMPAIR_DEBUG_HTTP") != "" {
		fmt.Fprintf(os.Stderr, "[ADMIN] /admin/groups raw: %s\n", string(raw))
	}
	// Try object with groups field (array of objects)
	var obj struct {
		Groups []struct {
			ID      string `json:"id"`
			GroupID string `json:"group_id"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Groups) > 0 {
		ids := make([]string, 0, len(obj.Groups))
		for _, g := range obj.Groups {
			id := g.ID
			if id == "" {
				id = g.GroupID
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	// Try array of objects
	var arr []struct {
		ID      string `json:"id"`
		GroupID string `json:"group_id"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		ids := make([]string, 0, len(arr))
		for _, g := range arr {
			id := g.ID
			if id == "" {
				id = g.GroupID
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	// Try array of strings
	var ids []string
	if err := json.Unmarshal(raw, &ids); err == nil && len(ids) > 0 {
		return ids, nil
	}
	// Try object with group_ids or ids as array of strings
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err == nil {
		for _, k := range []string{"group_ids", "ids", "groups", "items", "data"} {
			if v, ok := m[k]; ok {
				var sids []string
				if err := json.Unmarshal(v, &sids); err == nil && len(sids) > 0 {
					return sids, nil
				}
				var objs []struct {
					ID      string `json:"id"`
					GroupID string `json:"group_id"`
				}
				if err := json.Unmarshal(v, &objs); err == nil && len(objs) > 0 {
					out := make([]string, 0, len(objs))
					for _, o := range objs {
						id := o.ID
						if id == "" {
							id = o.GroupID
						}
						if id != "" {
							out = append(out, id)
						}
					}
					return out, nil
				}
			}
		}
	}
	return []string{}, nil
}

func (c *Client) JoinGroup(groupID string) error {
	data := url.Values{"group_id": {groupID}}
	return c.postForm("/join_group", data, nil)
}

// Documents
type CreateDocResp struct {
	DocumentID string `json:"document_id"`
}

func (c *Client) CreateDoc(title, docType, content, groupIDs string, published bool) (CreateDocResp, error) {
	data := url.Values{}
	data.Set("document_title", title)
	if docType == "" {
		docType = "code-repo"
	}
	data.Set("document_type", docType)
	if content != "" {
		data.Set("document_content_b64", base64.StdEncoding.EncodeToString([]byte(content)))
	} else {
		data.Set("document_content", "")
	}
	data.Set("groups", groupIDs) // comma-separated group IDs
	if published {
		data.Set("is_published", "true")
	} else {
		data.Set("is_published", "false")
	}
	var out CreateDocResp
	if err := c.postForm("/create_doc", data, &out); err != nil {
		return CreateDocResp{}, err
	}
	return out, nil
}

// Processing
type ProcessDocResp struct {
	TaskID string `json:"task_id"`
}

type ProcessDocOptions struct {
	ChunkMode         string
	ReanalyzeExisting bool
}

func (c *Client) ProcessDoc(docID, text string, generateFeedback bool) (ProcessDocResp, error) {
	return c.ProcessDocWithOptions(docID, text, generateFeedback, ProcessDocOptions{})
}

func (c *Client) ProcessDocWithMode(docID, text string, generateFeedback bool, chunkMode string) (ProcessDocResp, error) {
	return c.ProcessDocWithOptions(docID, text, generateFeedback, ProcessDocOptions{
		ChunkMode: chunkMode,
	})
}

func (c *Client) ProcessDocWithOptions(docID, text string, generateFeedback bool, opts ProcessDocOptions) (ProcessDocResp, error) {
	data := url.Values{}
	data.Set("doc_id", docID)
	data.Set("doc_text", text)
	data.Set("doc_text_b64", base64.StdEncoding.EncodeToString([]byte(text)))
	if generateFeedback {
		data.Set("generate_feedback", "true")
	} else {
		data.Set("generate_feedback", "false")
	}
	if strings.TrimSpace(opts.ChunkMode) != "" {
		data.Set("chunk_mode", opts.ChunkMode)
	}
	if opts.ReanalyzeExisting {
		data.Set("reanalyze_existing", "true")
	}
	var out ProcessDocResp
	if err := c.postForm("/process_doc", data, &out); err != nil {
		return ProcessDocResp{}, err
	}
	return out, nil
}

type TaskStatus struct {
	Status  string         `json:"status"` // PENDING|STARTED|PROGRESS|SUCCESS|FAILED
	Result  interface{}    `json:"result,omitempty"`
	Message string         `json:"message,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// ---- Feedback & Documents ----
type Document struct {
	DocumentID       string      `json:"document_id"`
	UserID           string      `json:"user_id,omitempty"`
	AuthorID         string      `json:"author_id,omitempty"`
	Title            string      `json:"title"`
	Content          string      `json:"content,omitempty"`
	DocType          string      `json:"doc_type,omitempty"`
	DatetimeCreated  interface{} `json:"datetime_created,omitempty"`
	DatetimeModified interface{} `json:"datetime_modified,omitempty"`
	IsPublished      bool        `json:"is_published,omitempty"`
	Groups           []GroupItem `json:"groups,omitempty"`
}

type OCRUploadResp struct {
	TaskID string `json:"task_id"`
}

type OCRFileResult struct {
	TaskID string          `json:"task_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

type ListDocumentsOptions struct {
	GroupID    string
	OwnOnly    bool
	FilterType string
	Page       int
	PageSize   int
	AllPages   bool
}

func (c *Client) ListDocuments(groupID string, ownOnly bool) ([]Document, error) {
	opts := ListDocumentsOptions{
		GroupID:  groupID,
		OwnOnly:  ownOnly,
		Page:     1,
		PageSize: 100,
		AllPages: true,
	}
	return c.ListDocumentsWithOptions(opts)
}

func (c *Client) ListDocumentsWithOptions(opts ListDocumentsOptions) ([]Document, error) {
	page := opts.Page
	pageSize := opts.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	var results []Document
	for {
		q := url.Values{}
		if opts.GroupID != "" {
			q.Set("group_id", opts.GroupID)
		}
		if opts.OwnOnly {
			q.Set("own_documents_only", "true")
		} else {
			q.Set("own_documents_only", "false")
		}
		if opts.FilterType != "" {
			q.Set("filter_type", opts.FilterType)
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", strconv.Itoa(pageSize))
		path := "/load_documents"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var out struct {
			Documents  []Document `json:"documents"`
			TotalCount int        `json:"total_count"`
		}
		if err := c.get(path, &out); err != nil {
			return nil, err
		}
		if len(out.Documents) == 0 {
			break
		}
		results = append(results, out.Documents...)
		if !opts.AllPages {
			break
		}
		if out.TotalCount == 0 || len(results) >= out.TotalCount || len(out.Documents) < pageSize {
			break
		}
		page++
	}
	return results, nil
}

func (c *Client) GetDocumentByID(docID string) (Document, error) {
	var out Document
	if err := c.get("/load_document_by_id?document_id="+url.QueryEscape(docID), &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

func (c *Client) UploadOCRFile(path string, docID string) (OCRUploadResp, error) {
	fields := map[string]string{}
	if strings.TrimSpace(docID) != "" {
		fields["document_id"] = docID
	}
	var out OCRUploadResp
	if err := c.postMultipart("/upload/ocr-file", fields, "file", path, &out); err != nil {
		return OCRUploadResp{}, err
	}
	return out, nil
}

func (c *Client) GetOCRFileResult(taskID string) (OCRFileResult, error) {
	var out OCRFileResult
	if err := c.get("/ocr-file-result/"+url.PathEscape(taskID), &out); err != nil {
		return OCRFileResult{}, err
	}
	return out, nil
}

func (c *Client) GetDocumentByTitle(title string) (Document, error) {
	var out Document
	if err := c.get("/load_document?title="+url.QueryEscape(title), &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

type FeedbackEntry struct {
	FeedbackID   string              `json:"feedback_id"`
	ChunkID      string              `json:"chunk_id"`
	ChunkContent string              `json:"chunk_content,omitempty"`
	Feedback     string              `json:"feedback"`
	UserFeedback string              `json:"user_feedback,omitempty"`
	Timestamp    interface{}         `json:"timestamp"`
	References   []FeedbackReference `json:"references,omitempty"`
}

type FeedbackReference struct {
	ReferenceID string `json:"reference_id"`
	Type        string `json:"type"`
	DocumentID  string `json:"document_id"`
	NoteID      string `json:"note_id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Content     string `json:"content"`
}

func (c *Client) ListFeedback(docID string) ([]FeedbackEntry, error) {
	var out struct {
		DocumentID string          `json:"document_id"`
		Count      int             `json:"count"`
		Feedback   []FeedbackEntry `json:"feedback"`
	}
	if err := c.get("/documents/"+url.PathEscape(docID)+"/feedback", &out); err != nil {
		return nil, err
	}
	return out.Feedback, nil
}

type Chunk struct {
	ChunkID string `json:"chunk_id"`
	Content string `json:"content,omitempty"`
	Text    string `json:"text,omitempty"`
}

func (c *Client) LoadChunks(docID string) ([]Chunk, error) {
	var out []Chunk
	if err := c.get("/load_chunks?document_id="+url.QueryEscape(docID), &out); err != nil {
		return nil, err
	}
	return out, nil
}

type ReferenceDoc struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
}
type Reference struct {
	ReferenceID    string       `json:"reference_id"`
	SourceChunkID  string       `json:"source_chunk_id"`
	Document       ReferenceDoc `json:"document"`
	DocumentAuthor string       `json:"document_author"`
}

func (c *Client) LoadReferences(chunkID string) ([]Reference, error) {
	var out []Reference
	if err := c.get("/load_references?chunk_id="+url.QueryEscape(chunkID), &out); err != nil {
		return nil, err
	}
	return out, nil
}
func (c *Client) GetTaskStatus(taskID string) (TaskStatus, error) {
	if strings.TrimSpace(taskID) == "" {
		return TaskStatus{Status: "SUCCESS", Result: map[string]any{"detail": "processing completed locally"}}, nil
	}
	var out TaskStatus
	if err := c.get("/status/"+url.PathEscape(taskID), &out); err != nil {
		return TaskStatus{}, err
	}
	return out, nil
}

type ActivityItem struct {
	User       string      `json:"user"`
	UserID     string      `json:"user_id"`
	GroupID    string      `json:"group_id"`
	Action     string      `json:"action"`
	Object     string      `json:"object"`
	ObjectType string      `json:"object_type"`
	ObjectName string      `json:"object_name"`
	ObjectID   string      `json:"object_id"`
	Timestamp  interface{} `json:"timestamp"`
	Tooltip    string      `json:"tooltip,omitempty"`
}

type ActivityFeedResponse struct {
	Activities []ActivityItem `json:"activities"`
	TotalCount int            `json:"total_count"`
}

type ActivityFeedOptions struct {
	UserID     string
	Page       int
	PageSize   int
	IncludeOwn bool
}

func (c *Client) GetActivityFeed(opts ActivityFeedOptions) (ActivityFeedResponse, error) {
	q := url.Values{}
	if opts.UserID != "" {
		q.Set("user_id", opts.UserID)
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	q.Set("include_own_activities", strconv.FormatBool(opts.IncludeOwn))
	path := "/get_activity_feed"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out ActivityFeedResponse
	if err := c.get(path, &out); err != nil {
		return ActivityFeedResponse{}, err
	}
	return out, nil
}

type NotificationEvent struct {
	EventID        string      `json:"event_id"`
	UserID         string      `json:"user_id"`
	GroupID        string      `json:"group_id"`
	Intent         string      `json:"intent"`
	DedupeKey      string      `json:"dedupe_key"`
	TargetDocID    string      `json:"target_doc_id"`
	TargetChunkID  string      `json:"target_chunk_id"`
	PeerDocIDs     []string    `json:"peer_doc_ids"`
	Relevance      string      `json:"relevance"`
	Novelty        string      `json:"novelty"`
	Severity       string      `json:"severity"`
	Certainty      string      `json:"certainty"`
	DeliveryAction string      `json:"delivery_action"`
	Channel        string      `json:"channel"`
	ParseMode      string      `json:"parse_mode"`
	Model          string      `json:"model"`
	RunID          string      `json:"run_id"`
	DigestBucket   string      `json:"digest_bucket"`
	Rationale      []string    `json:"rationale"`
	EvidenceTarget string      `json:"evidence_target"`
	EvidencePeer   string      `json:"evidence_peer"`
	CreatedAt      interface{} `json:"created_at"`
	DeliveredAt    interface{} `json:"delivered_at"`
	AcknowledgedAt interface{} `json:"acknowledged_at"`
	DismissedAt    interface{} `json:"dismissed_at"`
}

type NotificationEventsResponse struct {
	Events     []NotificationEvent `json:"events"`
	TotalCount int                 `json:"total_count"`
}

type NotificationEventsOptions struct {
	GroupID             string
	Page                int
	PageSize            int
	IncludeAcknowledged bool
	IncludeDismissed    bool
}

func (c *Client) ListNotificationEvents(opts NotificationEventsOptions) (NotificationEventsResponse, error) {
	q := url.Values{}
	if opts.GroupID != "" {
		q.Set("group_id", opts.GroupID)
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	if opts.IncludeAcknowledged {
		q.Set("include_acknowledged", "true")
	}
	if opts.IncludeDismissed {
		q.Set("include_dismissed", "true")
	}
	path := "/notification_events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out NotificationEventsResponse
	if err := c.get(path, &out); err != nil {
		return NotificationEventsResponse{}, err
	}
	return out, nil
}

func (c *Client) AcknowledgeNotificationEvent(eventID string) error {
	return c.post("/notification_events/"+url.PathEscape(eventID)+"/acknowledge", map[string]string{}, nil)
}

func (c *Client) DismissNotificationEvent(eventID string) error {
	return c.post("/notification_events/"+url.PathEscape(eventID)+"/dismiss", map[string]string{}, nil)
}

func (c *Client) ShareNotificationEvent(eventID, note string) error {
	payload := map[string]string{"note": note}
	return c.post("/notification_events/"+url.PathEscape(eventID)+"/share", payload, nil)
}

type NotificationPreferences struct {
	PreferencesID                       string   `json:"preferences_id"`
	EmailDigestEnabled                  bool     `json:"email_digest_enabled"`
	EmailDigestFrequency                string   `json:"email_digest_frequency"`
	PushNotificationsEnabled            bool     `json:"push_notifications_enabled"`
	DigestBucketsEnabled                []string `json:"digest_buckets_enabled"`
	QuietHoursStart                     string   `json:"quiet_hours_start"`
	QuietHoursEnd                       string   `json:"quiet_hours_end"`
	MaxDailyPushEmails                  int      `json:"max_daily_push_emails"`
	AccountEmail                        string   `json:"account_email"`
	NotificationDeliveryEmail           string   `json:"notification_delivery_email"`
	NotificationDeliveryEmailPending    string   `json:"notification_delivery_email_pending"`
	NotificationDeliveryEmailVerified   bool     `json:"notification_delivery_email_verified"`
	NotificationDeliveryEmailVerifiedAt string   `json:"notification_delivery_email_verified_at"`
	NotificationDeliveryEmailEffective  string   `json:"notification_delivery_email_effective"`
	NotificationDeliveryEmailSource     string   `json:"notification_delivery_email_source"`
}

type NotificationPreferencesUpdate map[string]any

func (c *Client) GetNotificationPreferences() (NotificationPreferences, error) {
	var out NotificationPreferences
	if err := c.get("/notification_preferences", &out); err != nil {
		return NotificationPreferences{}, err
	}
	return out, nil
}

func (c *Client) UpdateNotificationPreferences(update NotificationPreferencesUpdate) error {
	return c.post("/notification_preferences", map[string]any(update), nil)
}

func (c *Client) RequestNotificationDeliveryEmail(email string) error {
	return c.post("/notification_preferences/delivery_email", map[string]any{"email": email}, nil)
}

func (c *Client) ClearNotificationDeliveryEmail() error {
	return c.post("/notification_preferences/delivery_email/clear", map[string]any{}, nil)
}

func (c *Client) RateFeedback(feedbackID, value string) error {
	body := map[string]any{}
	if strings.TrimSpace(value) == "" {
		body["user_feedback"] = nil
	} else {
		body["user_feedback"] = value
	}
	return c.post("/feedback/"+url.PathEscape(feedbackID)+"/rate", body, nil)
}

func (c *Client) HideFeedback(feedbackID string, hidden bool) error {
	data := url.Values{"is_hidden": {strconv.FormatBool(hidden)}}
	return c.postForm("/feedback/"+url.PathEscape(feedbackID)+"/hide", data, nil)
}

type NoteAuthor struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Note struct {
	NoteID          string      `json:"note_id"`
	DocumentID      string      `json:"document_id"`
	AuthorID        string      `json:"author_id"`
	GroupID         string      `json:"group_id,omitempty"`
	Content         string      `json:"content"`
	DatetimeCreated interface{} `json:"datetime_created"`
	Author          *NoteAuthor `json:"author,omitempty"`
}

func (c *Client) CreateNote(documentID, content, groupID string) (Note, error) {
	data := url.Values{"content": {content}}
	if strings.TrimSpace(groupID) != "" {
		data.Set("group_id", groupID)
	}
	var out Note
	if err := c.postForm("/documents/"+url.PathEscape(documentID)+"/notes", data, &out); err != nil {
		return Note{}, err
	}
	return out, nil
}

func (c *Client) ListNotes(documentID string) ([]Note, error) {
	var out []Note
	if err := c.get("/documents/"+url.PathEscape(documentID)+"/notes", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetNote(noteID string) (Note, error) {
	var out Note
	if err := c.get("/notes/"+url.PathEscape(noteID), &out); err != nil {
		return Note{}, err
	}
	return out, nil
}
