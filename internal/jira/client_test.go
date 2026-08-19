package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
)

func TestHTTPClientPaginatesOrganizationsAndMembers(t *testing.T) {
	var serverURL string
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if user, password, ok := request.BasicAuth(); !ok || user != "admin@example.com" || password != "secret" {
			t.Fatalf("unexpected basic auth")
		}
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/rest/servicedeskapi/organization" && request.URL.Query().Get("start") == "0":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 0, "limit": 50, "isLastPage": false, "values": []any{},
				"_links": map[string]string{"next": serverURL + "/rest/servicedeskapi/organization?start=50&limit=50"},
			})
		case request.URL.Path == "/rest/servicedeskapi/organization" && request.URL.Query().Get("start") == "50":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 50, "limit": 50, "isLastPage": true,
				"values": []any{map[string]string{"id": "org-1", "name": "Acme"}},
			})
		case request.URL.Path == "/rest/servicedeskapi/organization/org-1/user" && request.URL.Query().Get("start") == "0":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 0, "limit": 1, "isLastPage": false,
				"values": []any{map[string]string{"accountId": "account-1", "emailAddress": "first@example.com"}},
				"_links": map[string]string{"next": serverURL + "/rest/servicedeskapi/organization/org-1/user?start=1&limit=1"},
			})
		case request.URL.Path == "/rest/servicedeskapi/organization/org-1/user" && request.URL.Query().Get("start") == "1":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 1, "limit": 1, "isLastPage": true,
				"values": []any{map[string]string{"accountId": "account-2", "emailAddress": "second@example.com"}},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	organization, existing, err := client.EnsureOrganization(context.Background(), "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if !existing || organization.ID != "org-1" {
		t.Fatalf("organization = %#v, existing = %v", organization, existing)
	}
	members, err := client.ListOrganizationUsers(context.Background(), organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[1].AccountID != "account-2" {
		t.Fatalf("members = %#v", members)
	}
}

func TestHTTPClientEnsuresExistingAndNewCustomers(t *testing.T) {
	var createCalls int
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/rest/servicedeskapi/servicedesk/SUP/customer":
			if strings.Contains(request.URL.Query().Get("query"), "existing@example.com") {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"start": 0, "limit": 50, "isLastPage": true,
					"values": []any{map[string]string{"accountId": "account-existing", "emailAddress": "existing@example.com"}},
				})
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"start": 0, "limit": 50, "isLastPage": true, "values": []any{}})
		case request.Method == http.MethodGet && request.URL.Path == "/rest/api/3/user/search":
			_ = json.NewEncoder(response).Encode([]any{})
		case request.Method == http.MethodPost && request.URL.Path == "/rest/servicedeskapi/customer":
			createCalls++
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(map[string]string{"accountId": "account-new"})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	existing, wasExisting, err := client.EnsureCustomer(context.Background(), "SUP", model.CustomerUser{Email: "existing@example.com", DisplayName: "Existing"})
	if err != nil || !wasExisting || existing.AccountID != "account-existing" {
		t.Fatalf("existing = %#v, wasExisting = %v, err = %v", existing, wasExisting, err)
	}
	created, wasExisting, err := client.EnsureCustomer(context.Background(), "SUP", model.CustomerUser{Email: "new@example.com", DisplayName: "New"})
	if err != nil || wasExisting || created.AccountID != "account-new" || createCalls != 1 {
		t.Fatalf("created = %#v, wasExisting = %v, createCalls = %d, err = %v", created, wasExisting, createCalls, err)
	}
}

func TestHTTPClientReusesExistingJiraAccount(t *testing.T) {
	var createCalls int
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/rest/servicedeskapi/servicedesk/SUP/customer":
			_ = json.NewEncoder(response).Encode(map[string]any{"start": 0, "limit": 50, "isLastPage": true, "values": []any{}})
		case request.Method == http.MethodGet && request.URL.Path == "/rest/api/3/user/search":
			_ = json.NewEncoder(response).Encode([]any{map[string]string{
				"accountId": "account-existing", "displayName": "Existing User",
			}})
		case request.Method == http.MethodPost && request.URL.Path == "/rest/servicedeskapi/customer":
			createCalls++
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]string{"errorMessage": "account already exists"})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	customer, existing, err := client.EnsureCustomer(context.Background(), "SUP", model.CustomerUser{Email: "existing@example.com"})
	if err != nil || !existing || customer.AccountID != "account-existing" || createCalls != 0 {
		t.Fatalf("customer = %#v, existing = %v, createCalls = %d, err = %v", customer, existing, createCalls, err)
	}
}

func TestHTTPClientReusesExistingCustomerWithHiddenEmail(t *testing.T) {
	var createCalls int
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/rest/servicedeskapi/servicedesk/SUP/customer":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 0, "limit": 50, "isLastPage": true,
				"values": []any{map[string]string{"accountId": "account-existing"}},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/rest/api/3/user/search":
			_ = json.NewEncoder(response).Encode([]any{})
		case request.Method == http.MethodPost && request.URL.Path == "/rest/servicedeskapi/customer":
			createCalls++
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]string{"errorMessage": "account already exists"})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	customer, existing, err := client.EnsureCustomer(context.Background(), "SUP", model.CustomerUser{Email: "existing@example.com"})
	if err != nil || !existing || customer.AccountID != "account-existing" || createCalls != 0 {
		t.Fatalf("customer = %#v, existing = %v, createCalls = %d, err = %v", customer, existing, createCalls, err)
	}
}

func TestHTTPClientIncludesJiraErrorDetails(t *testing.T) {
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"errorMessages":["customer already exists"]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.EnsureOrganization(context.Background(), "Acme")
	if err == nil || !strings.Contains(err.Error(), "customer already exists") {
		t.Fatalf("error = %v, want Jira response details", err)
	}
}

func TestHTTPClientAddsOrganizationUser(t *testing.T) {
	var gotBody map[string]any
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/rest/servicedeskapi/organization/org-1/user" {
			http.NotFound(response, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AddUserToOrganization(context.Background(), "org-1", "account-1"); err != nil {
		t.Fatal(err)
	}
	accountIDs, ok := gotBody["accountIds"].([]any)
	if !ok || len(accountIDs) != 1 || accountIDs[0] != "account-1" {
		t.Fatalf("request body = %#v", gotBody)
	}
}

func TestHTTPClientListsUserOrganizations(t *testing.T) {
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/rest/servicedeskapi/organization" || request.URL.Query().Get("accountId") != "account-1" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"start": 0, "limit": 50, "isLastPage": true,
			"values": []any{map[string]string{"id": "org-1", "name": "Acme"}},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	organizations, err := client.ListUserOrganizations(context.Background(), "account-1")
	if err != nil || len(organizations) != 1 || organizations[0].ID != "org-1" {
		t.Fatalf("organizations = %#v, err = %v", organizations, err)
	}
}

func TestHTTPClientListsServiceDeskCustomers(t *testing.T) {
	var serverURL string
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/rest/servicedeskapi/servicedesk/SUP/customer" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Query().Get("start") {
		case "0":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 0, "limit": 1, "isLastPage": false,
				"values": []any{map[string]string{"accountId": "account-1", "emailAddress": "first@example.com"}},
				"_links": map[string]string{"next": serverURL + "/rest/servicedeskapi/servicedesk/SUP/customer?start=1&limit=1"},
			})
		case "1":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"start": 1, "limit": 1, "isLastPage": true,
				"values": []any{map[string]string{"accountId": "account-2", "emailAddress": "second@example.com"}},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	customers, err := client.ListServiceDeskCustomers(context.Background(), "SUP")
	if err != nil || len(customers) != 2 || customers[1].AccountID != "account-2" {
		t.Fatalf("customers = %#v, err = %v", customers, err)
	}
}

func TestHTTPClientRemovesOrganizationUser(t *testing.T) {
	var gotBody map[string]any
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/rest/servicedeskapi/organization/org-1/user" {
			http.NotFound(response, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveUserFromOrganization(context.Background(), "org-1", "account-1"); err != nil {
		t.Fatal(err)
	}
	accountIDs, ok := gotBody["accountIds"].([]any)
	if !ok || len(accountIDs) != 1 || accountIDs[0] != "account-1" {
		t.Fatalf("request body = %#v", gotBody)
	}
}

func TestHTTPClientRemovesCustomerFromServiceDesk(t *testing.T) {
	var gotBody map[string]any
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/rest/servicedeskapi/servicedesk/SUP/customer" {
			http.NotFound(response, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveCustomerFromServiceDesk(context.Background(), "SUP", "account-1"); err != nil {
		t.Fatal(err)
	}
	accountIDs, ok := gotBody["accountIds"].([]any)
	if !ok || len(accountIDs) != 1 || accountIDs[0] != "account-1" {
		t.Fatalf("request body = %#v", gotBody)
	}
}

func TestHTTPClientClassifiesServiceDeskOpenAccessError(t *testing.T) {
	server := newIPv4Server(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/rest/servicedeskapi/servicedesk/SUP/customer" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(response).Encode(map[string]any{
			"errorMessage": "Customers cannot be removed from this service space because it has open access enabled",
			"i18nErrorMessage": map[string]any{
				"i18nKey":    "sd.jsm.error.servicedesk.customer.remove.servicedesk.open.access.galaxia",
				"parameters": []string{},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, UserEmail: "admin@example.com", APIToken: "secret", HTTPClient: server.Client, allowInsecureForTest: true})
	if err != nil {
		t.Fatal(err)
	}
	err = client.RemoveCustomerFromServiceDesk(context.Background(), "SUP", "account-1")
	if !errors.Is(err, ErrServiceDeskOpenAccessEnabled) {
		t.Fatalf("error = %v, want open-access classification", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.I18nKey == "" {
		t.Fatalf("error = %v, want underlying API error with i18n key", err)
	}
}

func TestHTTPClientRejectsNonHTTPSBaseURL(t *testing.T) {
	if _, err := NewClient(Config{BaseURL: "http://jira.example", UserEmail: "user", APIToken: "token"}); err == nil {
		t.Fatal("expected HTTPS validation error")
	}
}

func TestURLQueryEscapingIsSafe(t *testing.T) {
	if got := url.QueryEscape("user+tag@example.com"); got != "user%2Btag%40example.com" {
		t.Fatalf("QueryEscape = %q", got)
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) *testServer {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return &testServer{URL: "http://" + listener.Addr().String(), Client: &http.Client{}, server: server}
}

type testServer struct {
	URL    string
	Client *http.Client
	server *http.Server
}

func (s *testServer) Close() { _ = s.server.Close() }
