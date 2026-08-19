package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/jira"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
)

type membershipRemoval struct {
	organizationID string
	accountID      string
}

type fakeClient struct {
	organization         jira.Organization
	organizationExists   bool
	organizationUsers    []jira.Customer
	serviceDeskCustomers []jira.Customer
	userOrganizations    map[string][]jira.Organization
	customerExists       bool
	customer             jira.Customer
	ensureCalls          int
	added                []string
	removed              []membershipRemoval
	removedCustomers     []string
	removeCustomerError  error
	addError             error
}

func (f *fakeClient) EnsureOrganization(context.Context, string) (jira.Organization, bool, error) {
	return f.organization, f.organizationExists, nil
}

func (f *fakeClient) LinkOrganization(context.Context, string, string) error { return nil }

func (f *fakeClient) ListOrganizationUsers(context.Context, string) ([]jira.Customer, error) {
	return f.organizationUsers, nil
}

func (f *fakeClient) ListServiceDeskCustomers(context.Context, string) ([]jira.Customer, error) {
	return f.serviceDeskCustomers, nil
}

func (f *fakeClient) ListUserOrganizations(_ context.Context, accountID string) ([]jira.Organization, error) {
	return f.userOrganizations[accountID], nil
}

func (f *fakeClient) EnsureCustomer(context.Context, string, model.CustomerUser) (jira.Customer, bool, error) {
	f.ensureCalls++
	return f.customer, f.customerExists, nil
}

func (f *fakeClient) AddUserToOrganization(_ context.Context, _, accountID string) error {
	if f.addError != nil {
		return f.addError
	}
	f.added = append(f.added, accountID)
	return nil
}

func (f *fakeClient) RemoveUserFromOrganization(_ context.Context, organizationID, accountID string) error {
	f.removed = append(f.removed, membershipRemoval{organizationID: organizationID, accountID: accountID})
	return nil
}

func (f *fakeClient) RemoveCustomerFromServiceDesk(_ context.Context, _, accountID string) error {
	f.removedCustomers = append(f.removedCustomers, accountID)
	return f.removeCustomerError
}

func testReconciler(client *fakeClient) *Reconciler {
	return testReconcilerWithMode(client, MembershipModeAdditive)
}

func testReconcilerWithMode(client *fakeClient, membershipMode string) *Reconciler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(client, logger, Options{OrganizationName: "Acme", ServiceDeskID: "SUP", MembershipMode: membershipMode})
}

func TestSyncCreatesOrganizationCustomerAndMembership(t *testing.T) {
	client := &fakeClient{
		organization: jira.Organization{ID: "1", Name: "Acme"},
		customer:     jira.Customer{AccountID: "account-1"},
	}
	summary, err := testReconciler(client).Sync(context.Background(), []model.CustomerUser{{Email: "USER@EXAMPLE.COM"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.OrganizationStatus != "created" || summary.CustomersCreated != 1 || summary.MembershipsAdded != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(client.added) != 1 || client.added[0] != "account-1" {
		t.Fatalf("added = %#v", client.added)
	}
}

func TestSyncReusesExistingOrganizationAndMembership(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		organizationUsers:  []jira.Customer{{AccountID: "account-1", Email: "existing@example.com"}},
	}
	summary, err := testReconciler(client).Sync(context.Background(), []model.CustomerUser{{Email: "EXISTING@EXAMPLE.COM"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.OrganizationStatus != "existing" || summary.CustomersExisting != 1 || summary.MembershipsAdded != 0 || client.ensureCalls != 0 {
		t.Fatalf("unexpected summary/client: %#v %#v", summary, client)
	}
}

func TestSyncReusesMembershipWhenJiraHidesMemberEmail(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		organizationUsers:  []jira.Customer{{AccountID: "account-1"}},
		customerExists:     true,
		customer:           jira.Customer{AccountID: "account-1"},
	}
	summary, err := testReconciler(client).Sync(context.Background(), []model.CustomerUser{{Email: "hidden@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.CustomersExisting != 1 || summary.MembershipsAdded != 0 || client.ensureCalls != 1 {
		t.Fatalf("unexpected summary/client: %#v %#v", summary, client)
	}
}

func TestSyncAddsCustomerThatBelongsToAnotherOrganization(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		customerExists:     true,
		customer:           jira.Customer{AccountID: "account-2"},
	}
	summary, err := testReconciler(client).Sync(context.Background(), []model.CustomerUser{{Email: "other@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.CustomersExisting != 1 || summary.MembershipsAdded != 1 || len(client.added) != 1 {
		t.Fatalf("unexpected summary/client: %#v %#v", summary, client)
	}
}

func TestSyncAuthoritativeRemovesStaleMembersAndOrphanCustomers(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		organizationUsers: []jira.Customer{
			{AccountID: "account-keep", Email: "keep@example.com"},
			{AccountID: "account-stale", Email: "stale@example.com"},
			{AccountID: "account-shared", Email: "shared@example.com"},
		},
		serviceDeskCustomers: []jira.Customer{
			{AccountID: "account-keep", Email: "keep@example.com"},
			{AccountID: "account-stale", Email: "stale@example.com"},
			{AccountID: "account-shared", Email: "shared@example.com"},
		},
		userOrganizations: map[string][]jira.Organization{
			"account-stale":  {{ID: "1", Name: "Acme"}},
			"account-shared": {{ID: "other-org", Name: "Other"}},
		},
		customerExists: true,
		customer:       jira.Customer{AccountID: "account-keep"},
	}
	summary, err := testReconcilerWithMode(client, MembershipModeAuthoritative).Sync(context.Background(), []model.CustomerUser{{Email: "keep@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.CustomersExisting != 1 || summary.MembershipsAdded != 0 || summary.MembershipsRemoved != 2 || summary.CustomersRemoved != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(client.removed) != 2 || client.removed[0].accountID != "account-stale" || client.removed[1].accountID != "account-shared" {
		t.Fatalf("removed memberships = %#v", client.removed)
	}
	if len(client.removedCustomers) != 1 || client.removedCustomers[0] != "account-stale" {
		t.Fatalf("removed customers = %#v", client.removedCustomers)
	}
}

func TestSyncAuthoritativeRemovesPreExistingOrphanCustomers(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		organizationUsers:  []jira.Customer{{AccountID: "account-keep", Email: "keep@example.com"}},
		serviceDeskCustomers: []jira.Customer{
			{AccountID: "account-keep", Email: "keep@example.com"},
			{AccountID: "account-orphan", Email: "orphan@example.com"},
		},
		userOrganizations: map[string][]jira.Organization{
			"account-orphan": {},
		},
		customerExists: true,
		customer:       jira.Customer{AccountID: "account-keep"},
	}

	summary, err := testReconcilerWithMode(client, MembershipModeAuthoritative).Sync(context.Background(), []model.CustomerUser{{Email: "keep@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.MembershipsRemoved != 0 || summary.CustomersRemoved != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(client.removedCustomers) != 1 || client.removedCustomers[0] != "account-orphan" {
		t.Fatalf("removed customers = %#v", client.removedCustomers)
	}
}

func TestSyncAuthoritativeStopsAfterOpenAccessDeletionFailure(t *testing.T) {
	client := &fakeClient{
		organization:       jira.Organization{ID: "1", Name: "Acme"},
		organizationExists: true,
		organizationUsers:  []jira.Customer{{AccountID: "account-keep", Email: "keep@example.com"}},
		serviceDeskCustomers: []jira.Customer{
			{AccountID: "account-keep", Email: "keep@example.com"},
			{AccountID: "account-orphan-1", Email: "orphan-1@example.com"},
			{AccountID: "account-orphan-2", Email: "orphan-2@example.com"},
			{AccountID: "account-orphan-3", Email: "orphan-3@example.com"},
		},
		customerExists:      true,
		customer:            jira.Customer{AccountID: "account-keep"},
		removeCustomerError: jira.ErrServiceDeskOpenAccessEnabled,
	}

	_, err := testReconcilerWithMode(client, MembershipModeAuthoritative).Sync(context.Background(), []model.CustomerUser{{Email: "keep@example.com"}})
	if err == nil {
		t.Fatal("expected open-access prerequisite failure")
	}
	if len(client.removedCustomers) != 1 {
		t.Fatalf("removed customers = %#v, want exactly one attempted deletion", client.removedCustomers)
	}
	if strings.Count(err.Error(), serviceDeskOpenAccessFailure) != 1 {
		t.Fatalf("error = %v, want one deterministic prerequisite failure", err)
	}
}

func TestSyncReturnsErrorAfterPartialJiraFailure(t *testing.T) {
	client := &fakeClient{
		organization: jira.Organization{ID: "1", Name: "Acme"},
		customer:     jira.Customer{AccountID: "account-1"},
		addError:     errors.New("Jira unavailable"),
	}
	summary, err := testReconciler(client).Sync(context.Background(), []model.CustomerUser{{Email: "user@example.com"}})
	if err == nil {
		t.Fatal("expected Jira failure")
	}
	if summary.CustomersCreated != 1 || summary.MembershipsAdded != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}
