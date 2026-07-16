package digitalocean

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.digitalocean.com/v2"

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int    `json:"status_code"`
	ID         string `json:"id"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
}

func (e *APIError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("digitalocean %s: %s", e.ID, e.Message)
	}
	return fmt.Sprintf("digitalocean HTTP %d: %s", e.StatusCode, e.Message)
}

func IsStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

type RateInfo struct {
	Limit     *int
	Remaining *int
	ResetAt   *time.Time
}

func New(token string) *Client {
	return &Client{
		token:   strings.TrimSpace(token),
		baseURL: DefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
			},
		},
	}
}

func NewWithBaseURL(token, baseURL string) *Client {
	client := New(token)
	client.baseURL = strings.TrimRight(baseURL, "/")
	return client
}

type Account struct {
	DropletLimit    int    `json:"droplet_limit"`
	FloatingIPLimit int    `json:"floating_ip_limit"`
	Email           string `json:"email"`
	UUID            string `json:"uuid"`
	EmailVerified   bool   `json:"email_verified"`
	Status          string `json:"status"`
	StatusMessage   string `json:"status_message"`
	Team            *struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	} `json:"team,omitempty"`
}

type Balance struct {
	MonthToDateUsage   string    `json:"month_to_date_usage"`
	AccountBalance     string    `json:"account_balance"`
	MonthToDateBalance string    `json:"month_to_date_balance"`
	GeneratedAt        time.Time `json:"generated_at"`
}

type Region struct {
	Name      string   `json:"name"`
	Slug      string   `json:"slug"`
	Features  []string `json:"features"`
	Available bool     `json:"available"`
	Sizes     []string `json:"sizes"`
}

type Size struct {
	Slug         string   `json:"slug"`
	Memory       int      `json:"memory"`
	VCPUs        int      `json:"vcpus"`
	Disk         int      `json:"disk"`
	Transfer     float64  `json:"transfer"`
	PriceMonthly float64  `json:"price_monthly"`
	PriceHourly  float64  `json:"price_hourly"`
	Regions      []string `json:"regions"`
	Available    bool     `json:"available"`
	Description  string   `json:"description"`
}

type Image struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Distribution  string   `json:"distribution"`
	Slug          *string  `json:"slug"`
	Public        bool     `json:"public"`
	Regions       []string `json:"regions"`
	MinDiskSize   int      `json:"min_disk_size"`
	SizeGigabytes float64  `json:"size_gigabytes"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	Tags          []string `json:"tags"`
}

type Networks struct {
	V4 []struct {
		IPAddress string `json:"ip_address"`
		Netmask   string `json:"netmask"`
		Gateway   string `json:"gateway"`
		Type      string `json:"type"`
	} `json:"v4"`
	V6 []struct {
		IPAddress string `json:"ip_address"`
		Netmask   int    `json:"netmask"`
		Gateway   string `json:"gateway"`
		Type      string `json:"type"`
	} `json:"v6"`
}

type Droplet struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Memory      int             `json:"memory"`
	VCPUs       int             `json:"vcpus"`
	Disk        int             `json:"disk"`
	Locked      bool            `json:"locked"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"created_at"`
	Features    []string        `json:"features"`
	BackupIDs   []int64         `json:"backup_ids"`
	SnapshotIDs []int64         `json:"snapshot_ids"`
	Image       Image           `json:"image"`
	Size        Size            `json:"size"`
	SizeSlug    string          `json:"size_slug"`
	Networks    Networks        `json:"networks"`
	Region      Region          `json:"region"`
	Tags        []string        `json:"tags"`
	VolumeIDs   []string        `json:"volume_ids"`
	VPCUUID     string          `json:"vpc_uuid"`
	Raw         json.RawMessage `json:"-"`
}

type Action struct {
	ID           int64      `json:"id"`
	Status       string     `json:"status"`
	Type         string     `json:"type"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	ResourceID   int64      `json:"resource_id"`
	ResourceType string     `json:"resource_type"`
	RegionSlug   string     `json:"region_slug"`
}

type CreateDropletRequest struct {
	Names            []string       `json:"-"`
	Region           string         `json:"region,omitempty"`
	Size             string         `json:"size"`
	Image            any            `json:"image"`
	SSHKeys          []any          `json:"ssh_keys,omitempty"`
	Backups          bool           `json:"backups,omitempty"`
	BackupPolicy     map[string]any `json:"backup_policy,omitempty"`
	IPv6             bool           `json:"ipv6,omitempty"`
	Monitoring       bool           `json:"monitoring,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	UserData         string         `json:"user_data,omitempty"`
	VPCUUID          string         `json:"vpc_uuid,omitempty"`
	PublicNetworking *bool          `json:"public_networking,omitempty"`
	WithDropletAgent *bool          `json:"with_droplet_agent,omitempty"`
}

type CreateDropletResponse struct {
	Droplets []Droplet
	Actions  []Action
}

func (c *Client) Account(ctx context.Context) (Account, RateInfo, error) {
	var response struct {
		Account Account `json:"account"`
	}
	rate, err := c.request(ctx, http.MethodGet, "/account", nil, &response)
	return response.Account, rate, err
}

func (c *Client) Balance(ctx context.Context) (Balance, RateInfo, error) {
	var balance Balance
	rate, err := c.request(ctx, http.MethodGet, "/customers/my/balance", nil, &balance)
	return balance, rate, err
}

func (c *Client) ListDroplets(ctx context.Context) ([]Droplet, RateInfo, error) {
	items := make([]Droplet, 0)
	path := "/droplets?per_page=200&page=1"
	var lastRate RateInfo
	for path != "" {
		var response struct {
			Droplets []json.RawMessage `json:"droplets"`
			Links    pageLinks         `json:"links"`
		}
		rate, err := c.request(ctx, http.MethodGet, path, nil, &response)
		if err != nil {
			return nil, rate, err
		}
		lastRate = rate
		for _, raw := range response.Droplets {
			var droplet Droplet
			if err := json.Unmarshal(raw, &droplet); err != nil {
				return nil, rate, err
			}
			droplet.Raw = append([]byte(nil), raw...)
			items = append(items, droplet)
		}
		path = normalizeNext(response.Links.Pages.Next)
	}
	return items, lastRate, nil
}

func (c *Client) GetDroplet(ctx context.Context, id int64) (Droplet, RateInfo, error) {
	var response struct {
		Droplet json.RawMessage `json:"droplet"`
	}
	rate, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/droplets/%d", id), nil, &response)
	if err != nil {
		return Droplet{}, rate, err
	}
	var droplet Droplet
	if err := json.Unmarshal(response.Droplet, &droplet); err != nil {
		return Droplet{}, rate, err
	}
	droplet.Raw = append([]byte(nil), response.Droplet...)
	return droplet, rate, nil
}

func (c *Client) CreateDroplets(ctx context.Context, input CreateDropletRequest) (CreateDropletResponse, RateInfo, error) {
	if len(input.Names) == 0 || len(input.Names) > 10 {
		return CreateDropletResponse{}, RateInfo{}, errors.New("one to ten droplet names are required")
	}
	payload := map[string]any{
		"region": input.Region, "size": input.Size, "image": input.Image,
		"ssh_keys": input.SSHKeys, "backups": input.Backups, "ipv6": input.IPv6,
		"monitoring": input.Monitoring, "tags": input.Tags, "user_data": input.UserData,
		"vpc_uuid": input.VPCUUID,
	}
	if len(input.Names) == 1 {
		payload["name"] = input.Names[0]
	} else {
		payload["names"] = input.Names
	}
	if len(input.BackupPolicy) > 0 {
		payload["backup_policy"] = input.BackupPolicy
	}
	if input.PublicNetworking != nil {
		payload["public_networking"] = *input.PublicNetworking
	}
	if input.WithDropletAgent != nil {
		payload["with_droplet_agent"] = *input.WithDropletAgent
	}

	var response struct {
		Droplet  *Droplet  `json:"droplet"`
		Droplets []Droplet `json:"droplets"`
		Links    struct {
			Actions []Action `json:"actions"`
		} `json:"links"`
	}
	rate, err := c.request(ctx, http.MethodPost, "/droplets", payload, &response)
	if err != nil {
		return CreateDropletResponse{}, rate, err
	}
	droplets := response.Droplets
	if response.Droplet != nil {
		droplets = append(droplets, *response.Droplet)
	}
	return CreateDropletResponse{Droplets: droplets, Actions: response.Links.Actions}, rate, nil
}

func (c *Client) DeleteDroplet(ctx context.Context, id int64) (RateInfo, error) {
	return c.request(ctx, http.MethodDelete, fmt.Sprintf("/droplets/%d", id), nil, nil)
}

func (c *Client) DropletAction(ctx context.Context, dropletID int64, payload map[string]any) (Action, RateInfo, error) {
	var response struct {
		Action Action `json:"action"`
	}
	rate, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/droplets/%d/actions", dropletID), payload, &response)
	return response.Action, rate, err
}

func (c *Client) GetDropletAction(ctx context.Context, dropletID, actionID int64) (Action, RateInfo, error) {
	var response struct {
		Action Action `json:"action"`
	}
	rate, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/droplets/%d/actions/%d", dropletID, actionID), nil, &response)
	return response.Action, rate, err
}

func (c *Client) AssignProject(ctx context.Context, projectID string, dropletIDs []int64) (json.RawMessage, RateInfo, error) {
	resources := make([]string, 0, len(dropletIDs))
	for _, id := range dropletIDs {
		resources = append(resources, fmt.Sprintf("do:droplet:%d", id))
	}
	var response json.RawMessage
	rate, err := c.request(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/resources", map[string]any{"resources": resources}, &response)
	return response, rate, err
}

func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) (json.RawMessage, RateInfo, error) {
	if query != nil && len(query) > 0 {
		path += "?" + query.Encode()
	}
	var response json.RawMessage
	rate, err := c.request(ctx, http.MethodGet, path, nil, &response)
	return response, rate, err
}

func (c *Client) PostRaw(ctx context.Context, path string, payload any) (json.RawMessage, RateInfo, error) {
	var response json.RawMessage
	rate, err := c.request(ctx, http.MethodPost, path, payload, &response)
	return response, rate, err
}

func (c *Client) PutRaw(ctx context.Context, path string, payload any) (json.RawMessage, RateInfo, error) {
	var response json.RawMessage
	rate, err := c.request(ctx, http.MethodPut, path, payload, &response)
	return response, rate, err
}

func (c *Client) DeleteRaw(ctx context.Context, path string) (RateInfo, error) {
	return c.request(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) WaitDropletAction(ctx context.Context, dropletID, actionID int64, timeout time.Duration) (Action, error) {
	deadline := time.Now().Add(timeout)
	for {
		action, _, err := c.GetDropletAction(ctx, dropletID, actionID)
		if err != nil {
			return Action{}, err
		}
		switch action.Status {
		case "completed":
			return action, nil
		case "errored":
			return action, fmt.Errorf("digitalocean action %d errored", action.ID)
		}
		if time.Now().After(deadline) {
			return action, fmt.Errorf("digitalocean action %d timed out", action.ID)
		}
		select {
		case <-ctx.Done():
			return action, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func BuildRootPasswordCloudInit(password string) string {
	return fmt.Sprintf(`#cloud-config
disable_root: false
ssh_pwauth: true
chpasswd:
  expire: false
  users:
    - name: root
      password: %q
      type: text
runcmd:
  - [sh, -c, "grep -q '^PermitRootLogin' /etc/ssh/sshd_config && sed -i 's/^PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config"]
  - [sh, -c, "grep -q '^PasswordAuthentication' /etc/ssh/sshd_config && sed -i 's/^PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config"]
  - [sh, -c, "systemctl restart sshd || systemctl restart ssh || true"]
`, password)
}

type pageLinks struct {
	Pages struct {
		Next string `json:"next"`
	} `json:"pages"`
}

func normalizeNext(next string) string {
	if next == "" {
		return ""
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return parsed.RequestURI()[len("/v2"):]
}

func (c *Client) request(ctx context.Context, method, path string, payload any, target any) (RateInfo, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return RateInfo{}, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return RateInfo{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "cloud-account-manager/1.0")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return RateInfo{}, err
	}
	defer response.Body.Close()
	rate := parseRate(response.Header)
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return rate, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: response.StatusCode, RequestID: response.Header.Get("X-Request-ID")}
		_ = json.Unmarshal(data, apiErr)
		if apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(data))
		}
		return rate, apiErr
	}
	if target == nil || len(data) == 0 {
		return rate, nil
	}
	if raw, ok := target.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return rate, nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return rate, fmt.Errorf("decode DigitalOcean response: %w", err)
	}
	return rate, nil
}

func parseRate(header http.Header) RateInfo {
	var rate RateInfo
	if value, err := strconv.Atoi(header.Get("RateLimit-Limit")); err == nil && value > 0 {
		rate.Limit = &value
	}
	if value, err := strconv.Atoi(header.Get("RateLimit-Remaining")); err == nil && value >= 0 {
		rate.Remaining = &value
	}
	if value, err := strconv.ParseInt(header.Get("RateLimit-Reset"), 10, 64); err == nil && value > 0 {
		reset := time.Unix(value, 0)
		rate.ResetAt = &reset
	}
	return rate
}
