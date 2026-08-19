package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/jira"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
)

type fakeClient struct {
	organization       jira.Organization
	organizationExists bool
	organizationUsers  []jira.Customer
	customerExists     bool
	customer           jira.Customer
	ensureCalls        int
	added              []string
	addError           error
}

func (f *fakeClient) EnsureOrganization(context.Context, string) (jira.Organization, bool, error) {
	return f.organization, f.organizationExists, nil
}

func (f *fakeClient) LinkOrganization(context.Context, string, string) error { return nil }

func (f *fakeClient) ListOrganizationUsers(context.Context, string) ([]jira.Customer, error) {
	return f.organizationUsers, nil
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

func testReconciler(client *fakeClient) *Reconciler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(client, logger, Options{OrganizationName: "Acme", ServiceDeskID: "SUP", MembershipMode: "additive"})
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
