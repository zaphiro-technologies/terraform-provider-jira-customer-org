package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/filter"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/httpretry"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
)

const (
	organizationAPIPath  = "/rest/servicedeskapi/organization"
	serviceDeskAPIPath   = "/rest/servicedeskapi/servicedesk"
	initialPageQuery     = "?start=0&limit=50"
	pageStartQuery       = "?start="
	pageLimitQuery       = "&limit="
	openAccessI18nPrefix = "sd.jsm.error.servicedesk.customer.remove.servicedesk.open.access."
)

var ErrServiceDeskOpenAccessEnabled = errors.New("service desk has open access enabled")

type Client interface {
	EnsureOrganization(ctx context.Context, name string) (Organization, bool, error)
	LinkOrganization(ctx context.Context, serviceDeskID, organizationID string) error
	ListOrganizationUsers(ctx context.Context, organizationID string) ([]Customer, error)
	ListServiceDeskCustomers(ctx context.Context, serviceDeskID string) ([]Customer, error)
	ListUserOrganizations(ctx context.Context, accountID string) ([]Organization, error)
	EnsureCustomer(ctx context.Context, serviceDeskID string, user model.CustomerUser) (Customer, bool, error)
	AddUserToOrganization(ctx context.Context, organizationID, accountID string) error
	RemoveUserFromOrganization(ctx context.Context, organizationID, accountID string) error
	RemoveCustomerFromServiceDesk(ctx context.Context, serviceDeskID, accountID string) error
}

type Organization struct {
	ID   string
	Name string
}

type Customer struct {
	AccountID   string `json:"accountId"`
	Email       string `json:"emailAddress"`
	DisplayName string `json:"displayName"`
}

type Config struct {
	BaseURL              string
	UserEmail            string
	APIToken             string
	HTTPClient           *http.Client
	allowInsecureForTest bool
}

type HTTPClient struct {
	baseURL    string
	userEmail  string
	apiToken   string
	httpClient *http.Client
}

func NewClient(cfg Config) (*HTTPClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(cfg.UserEmail) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		return nil, fmt.Errorf("jira base URL, user email, and API token are required")
	}
	parsed, err := url.Parse(baseURL)
	validScheme := parsed.Scheme == "https" ||
		(cfg.allowInsecureForTest && parsed.Scheme == "http")

	if err != nil || parsed.Host == "" || !validScheme {
		return nil, fmt.Errorf("jira base URL must be an HTTPS URL")
	}
	return &HTTPClient{
		baseURL: baseURL, userEmail: cfg.UserEmail, apiToken: cfg.APIToken,
		httpClient: cfg.HTTPClient,
	}, nil
}

func NewClientFromEnv(httpClient *http.Client) (*HTTPClient, error) {
	return NewClientFromEnvWithBaseURL(httpClient, os.Getenv("JIRA_BASE_URL"))
}

func NewClientFromEnvWithBaseURL(httpClient *http.Client, baseURL string) (*HTTPClient, error) {
	userEmail, err := credentialFromEnv("JIRA_USER_EMAIL", "JIRA_USER_EMAIL_FILE", "jira_email.txt")
	if err != nil {
		return nil, err
	}
	apiToken, err := credentialFromEnv("JIRA_API_TOKEN", "JIRA_API_TOKEN_FILE", "jira_api_token.txt")
	if err != nil {
		return nil, err
	}
	return NewClient(Config{
		BaseURL: baseURL, UserEmail: userEmail, APIToken: apiToken, HTTPClient: httpClient,
	})
}

func (c *HTTPClient) EnsureOrganization(ctx context.Context, name string) (Organization, bool, error) {
	organization, found, err := c.findOrganization(ctx, name)
	if err != nil {
		return Organization{}, false, err
	}
	if found {
		return organization, true, nil
	}

	var response organizationDTO
	err = c.request(ctx, http.MethodPost, organizationAPIPath, map[string]string{"name": name}, &response, http.StatusCreated)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusConflict {
			organization, found, findErr := c.findOrganization(ctx, name)
			if findErr == nil && found {
				return organization, true, nil
			}
		}
		return Organization{}, false, err
	}
	if response.ID == "" {
		return Organization{}, false, fmt.Errorf("jira organization create response did not contain an ID")
	}
	return Organization(response), false, nil
}

func (c *HTTPClient) LinkOrganization(ctx context.Context, serviceDeskID, organizationID string) error {
	return c.request(ctx, http.MethodPost,
		path.Join(serviceDeskAPIPath, url.PathEscape(serviceDeskID), "organization"),
		map[string]string{"organizationId": organizationID}, nil, http.StatusNoContent, http.StatusOK)
}

func (c *HTTPClient) ListOrganizationUsers(ctx context.Context, organizationID string) ([]Customer, error) {
	var customers []Customer
	base := path.Join(organizationAPIPath, url.PathEscape(organizationID), "user")
	nextURL := c.endpoint(base) + initialPageQuery
	for nextURL != "" {
		var page userPage
		if err := c.requestURL(ctx, http.MethodGet, nextURL, nil, &page, http.StatusOK); err != nil {
			return nil, err
		}
		for _, user := range page.Values {
			customers = append(customers, Customer{
				AccountID: user.AccountID, Email: user.EmailAddress, DisplayName: user.DisplayName,
			})
		}
		nextURL = page.Links.Next
		if nextURL == "" && !page.IsLastPage {
			limit := page.Limit
			if limit <= 0 {
				limit = 50
			}
			nextURL = c.endpoint(base) + pageStartQuery + strconv.Itoa(page.Start+limit) + pageLimitQuery + strconv.Itoa(limit)
		}
	}
	return customers, nil
}

func (c *HTTPClient) ListServiceDeskCustomers(ctx context.Context, serviceDeskID string) ([]Customer, error) {
	base := path.Join(serviceDeskAPIPath, url.PathEscape(serviceDeskID), "customer")
	nextURL := c.endpoint(base) + initialPageQuery
	var customers []Customer
	for nextURL != "" {
		var page userPage
		if err := c.requestURL(ctx, http.MethodGet, nextURL, nil, &page, http.StatusOK); err != nil {
			return nil, err
		}
		for _, user := range page.Values {
			customers = append(customers, Customer{
				AccountID: user.AccountID, Email: user.EmailAddress, DisplayName: user.DisplayName,
			})
		}
		nextURL = page.Links.Next
		if nextURL == "" && !page.IsLastPage {
			limit := page.Limit
			if limit <= 0 {
				limit = 50
			}
			nextURL = c.endpoint(base) + pageStartQuery + strconv.Itoa(page.Start+limit) + pageLimitQuery + strconv.Itoa(limit)
		}
	}
	return customers, nil
}

func (c *HTTPClient) ListUserOrganizations(ctx context.Context, accountID string) ([]Organization, error) {
	base := organizationAPIPath
	nextURL := c.endpoint(base) + "?accountId=" + url.QueryEscape(accountID) + "&start=0&limit=50"
	var organizations []Organization
	for nextURL != "" {
		var page organizationPage
		if err := c.requestURL(ctx, http.MethodGet, nextURL, nil, &page, http.StatusOK); err != nil {
			return nil, err
		}
		for _, organization := range page.Values {
			organizations = append(organizations, Organization(organization))
		}
		nextURL = page.Links.Next
		if nextURL == "" && !page.IsLastPage {
			limit := page.Limit
			if limit <= 0 {
				limit = 50
			}
			nextURL = c.endpoint(base) + "?accountId=" + url.QueryEscape(accountID) + "&start=" + strconv.Itoa(page.Start+limit) + pageLimitQuery + strconv.Itoa(limit)
		}
	}
	return organizations, nil
}

func (c *HTTPClient) EnsureCustomer(ctx context.Context, serviceDeskID string, user model.CustomerUser) (Customer, bool, error) {
	email, ok := filter.NormalizeEmail(user.Email)
	if !ok {
		return Customer{}, false, fmt.Errorf("invalid customer email")
	}
	user.Email = email
	customer, found, err := c.findCustomer(ctx, serviceDeskID, email)
	if err != nil {
		return Customer{}, false, err
	}
	if found {
		return customer, true, nil
	}
	// Jira rejects customer creation when an Atlassian account already exists
	// for the email. Reuse that account instead of attempting to create a
	// duplicate customer. This also gives reconciliation an account ID when
	// organization membership responses omit emailAddress for privacy reasons.
	customer, found, err = c.findJiraUser(ctx, email)
	if err != nil {
		return Customer{}, false, err
	}
	if found {
		return customer, true, nil
	}

	var response Customer
	requestPath := "/rest/servicedeskapi/customer?strictConflictStatusCode=false"
	err = c.request(ctx, http.MethodPost, requestPath, map[string]string{
		"email": user.Email, "displayName": user.DisplayName,
	}, &response, http.StatusCreated)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusConflict {
			customer, found, findErr := c.findCustomer(ctx, serviceDeskID, user.Email)
			if findErr == nil && found {
				return customer, true, nil
			}
		}
		if apiErr, ok := err.(*APIError); ok && strings.Contains(strings.ToLower(apiErr.Details), "account already exists") {
			customer, found, findErr := c.findJiraUser(ctx, user.Email)
			if findErr == nil && found {
				return customer, true, nil
			}
		}
		return Customer{}, false, err
	}
	if response.AccountID == "" {
		return Customer{}, false, fmt.Errorf("jira customer create response did not contain an account ID")
	}
	response.Email = user.Email
	return response, false, nil
}

func (c *HTTPClient) AddUserToOrganization(ctx context.Context, organizationID, accountID string) error {
	return c.request(ctx, http.MethodPost,
		path.Join(organizationAPIPath, url.PathEscape(organizationID), "user"),
		map[string]any{"accountIds": []string{accountID}, "usernames": []string{}}, nil,
		http.StatusNoContent, http.StatusOK)
}

func (c *HTTPClient) RemoveUserFromOrganization(ctx context.Context, organizationID, accountID string) error {
	return c.request(ctx, http.MethodDelete,
		path.Join(organizationAPIPath, url.PathEscape(organizationID), "user"),
		map[string]any{"accountIds": []string{accountID}, "usernames": []string{}}, nil,
		http.StatusNoContent, http.StatusOK)
}

func (c *HTTPClient) RemoveCustomerFromServiceDesk(ctx context.Context, serviceDeskID, accountID string) error {
	err := c.request(ctx, http.MethodDelete,
		path.Join(serviceDeskAPIPath, url.PathEscape(serviceDeskID), "customer"),
		map[string]any{"accountIds": []string{accountID}, "usernames": []string{}}, nil,
		http.StatusNoContent, http.StatusOK)
	if err == nil {
		return nil
	}
	if isServiceDeskOpenAccessError(err) {
		return fmt.Errorf("%w: %w", ErrServiceDeskOpenAccessEnabled, err)
	}
	return err
}

type organizationDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type organizationPage struct {
	Start      int               `json:"start"`
	Limit      int               `json:"limit"`
	IsLastPage bool              `json:"isLastPage"`
	Values     []organizationDTO `json:"values"`
	Links      struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type userPage struct {
	Start      int       `json:"start"`
	Limit      int       `json:"limit"`
	IsLastPage bool      `json:"isLastPage"`
	Values     []userDTO `json:"values"`
	Links      struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type userDTO struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	DisplayName  string `json:"displayName"`
}

type jiraUserDTO struct {
	AccountID   string `json:"accountId"`
	Email       string `json:"emailAddress"`
	DisplayName string `json:"displayName"`
}

func (c *HTTPClient) findOrganization(ctx context.Context, name string) (Organization, bool, error) {
	base := organizationAPIPath
	nextURL := c.endpoint(base) + initialPageQuery
	for nextURL != "" {
		var page organizationPage
		if err := c.requestURL(ctx, http.MethodGet, nextURL, nil, &page, http.StatusOK); err != nil {
			return Organization{}, false, err
		}
		for _, candidate := range page.Values {
			if candidate.Name == name {
				return Organization(candidate), true, nil
			}
		}
		nextURL = page.Links.Next
		if nextURL == "" && !page.IsLastPage {
			limit := page.Limit
			if limit <= 0 {
				limit = 50
			}
			nextURL = c.endpoint(base) + pageStartQuery + strconv.Itoa(page.Start+limit) + pageLimitQuery + strconv.Itoa(limit)
		}
	}
	return Organization{}, false, nil
}

func (c *HTTPClient) findCustomer(ctx context.Context, serviceDeskID, email string) (Customer, bool, error) {
	base := path.Join(serviceDeskAPIPath, url.PathEscape(serviceDeskID), "customer")
	nextURL := c.endpoint(base) + "?query=" + url.QueryEscape(email) + "&start=0&limit=50"
	var candidates []Customer
	for nextURL != "" {
		var page userPage
		if err := c.requestURL(ctx, http.MethodGet, nextURL, nil, &page, http.StatusOK); err != nil {
			return Customer{}, false, err
		}
		customer, exact, pageCandidates := customerCandidates(page.Values, email)
		if exact {
			return customer, true, nil
		}
		candidates = append(candidates, pageCandidates...)
		nextURL = page.Links.Next
		if nextURL == "" && !page.IsLastPage {
			limit := page.Limit
			if limit <= 0 {
				limit = 50
			}
			nextURL = c.endpoint(base) + "?query=" + url.QueryEscape(email) + "&start=" + strconv.Itoa(page.Start+limit) + pageLimitQuery + strconv.Itoa(limit)
		}
	}
	if len(candidates) == 1 && candidates[0].Email == "" {
		candidates[0].Email = email
		return candidates[0], true, nil
	}
	return Customer{}, false, nil
}

func customerCandidates(values []userDTO, email string) (Customer, bool, []Customer) {
	var candidates []Customer
	for _, candidate := range values {
		customer := Customer{AccountID: candidate.AccountID, Email: candidate.EmailAddress, DisplayName: candidate.DisplayName}
		normalized, ok := filter.NormalizeEmail(candidate.EmailAddress)
		if ok && normalized == email {
			return customer, true, nil
		}
		if customer.AccountID != "" {
			candidates = append(candidates, customer)
		}
	}
	return Customer{}, false, candidates
}

func (c *HTTPClient) findJiraUser(ctx context.Context, email string) (Customer, bool, error) {
	var users []jiraUserDTO
	requestPath := "/rest/api/3/user/search?query=" + url.QueryEscape(email) + "&maxResults=50"
	if err := c.request(ctx, http.MethodGet, requestPath, nil, &users, http.StatusOK); err != nil {
		return Customer{}, false, err
	}

	for _, user := range users {
		if normalized, ok := filter.NormalizeEmail(user.Email); ok && normalized == email && user.AccountID != "" {
			return Customer{AccountID: user.AccountID, Email: email, DisplayName: user.DisplayName}, true, nil
		}
	}

	// Atlassian can omit emailAddress due to profile privacy. The exact email
	// query still identifies a single account in that case; accept it only
	// when the result is unambiguous.
	if len(users) == 1 && users[0].AccountID != "" {
		return Customer{AccountID: users[0].AccountID, Email: email, DisplayName: users[0].DisplayName}, true, nil
	}
	return Customer{}, false, nil
}

func (c *HTTPClient) request(ctx context.Context, method, requestPath string, body any, result any, statuses ...int) error {
	return c.requestURL(ctx, method, c.endpoint(requestPath), body, result, statuses...)
}

func (c *HTTPClient) requestURL(ctx context.Context, method, requestURL string, body any, result any, statuses ...int) error {
	encodedBody, err := json.Marshal(body)
	if body != nil && err != nil {
		return err
	}
	allowed := make(map[int]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	response, err := httpretry.Do(ctx, c.httpClient, func() (*http.Request, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(encodedBody)
		}
		request, err := http.NewRequest(method, requestURL, reader)
		if err != nil {
			return nil, err
		}
		request.SetBasicAuth(c.userEmail, c.apiToken)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-ExperimentalApi", "opt-in")
		return request, nil
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, ok := allowed[response.StatusCode]; !ok {
		requestURI, _ := url.Parse(requestURL)
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		if readErr != nil {
			return &APIError{
				StatusCode: response.StatusCode,
				Method:     method,
				Path:       requestURI.Path,
				Details:    fmt.Sprintf("unable to read Jira error response: %v", readErr),
			}
		}
		return &APIError{
			StatusCode: response.StatusCode,
			Method:     method,
			Path:       requestURI.Path,
			Details:    jiraErrorDetails(body),
			I18nKey:    jiraErrorI18nKey(body),
		}
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil && err != io.EOF {
		return fmt.Errorf("decode Jira response: %w", err)
	}
	return nil
}

type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Details    string
	I18nKey    string
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("Jira API %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	if e.Details != "" {
		message += ": " + e.Details
	}
	return message
}

func jiraErrorDetails(body []byte) string {
	details := strings.TrimSpace(string(body))
	if details == "" {
		return "Jira returned an empty error response"
	}
	if len(details) > 2048 {
		details = details[:2048] + "..."
	}
	return details
}

func jiraErrorI18nKey(body []byte) string {
	var response struct {
		I18nErrorMessage struct {
			I18nKey string `json:"i18nKey"`
		} `json:"i18nErrorMessage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ""
	}
	return response.I18nErrorMessage.I18nKey
}

func isServiceDeskOpenAccessError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && strings.HasPrefix(apiErr.I18nKey, openAccessI18nPrefix)
}

func (c *HTTPClient) endpoint(requestPath string) string {
	return c.baseURL + "/" + strings.TrimLeft(requestPath, "/")
}

func credentialFromEnv(envName, fileEnvName, defaultFile string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value, nil
	}
	fileName := strings.TrimSpace(os.Getenv(fileEnvName))
	if fileName == "" {
		fileName = defaultFile
	}
	contents, err := os.ReadFile(fileName)
	if err != nil {
		return "", fmt.Errorf("read %s or %s: %w", envName, fileName, err)
	}
	if value := strings.TrimSpace(string(contents)); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s is empty", envName)
}
