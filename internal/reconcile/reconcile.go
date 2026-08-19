package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/filter"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/jira"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
)

type Options struct {
	OrganizationName string
	ServiceDeskID    string
	MembershipMode   string
}

type Summary struct {
	OrganizationName   string
	OrganizationStatus string
	CustomersExisting  int
	CustomersCreated   int
	MembershipsAdded   int
}

type Reconciler struct {
	client  jira.Client
	logger  *slog.Logger
	options Options
}

func New(client jira.Client, logger *slog.Logger, options Options) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{client: client, logger: logger, options: options}
}

func (r *Reconciler) Sync(ctx context.Context, users []model.CustomerUser) (Summary, error) {
	summary := Summary{OrganizationName: r.options.OrganizationName}
	if r.options.MembershipMode != "additive" {
		return summary, fmt.Errorf("unsupported membership mode %q", r.options.MembershipMode)
	}
	organization, existing, err := r.client.EnsureOrganization(ctx, r.options.OrganizationName)
	if err != nil {
		return summary, fmt.Errorf("ensure Jira organization: %w", err)
	}
	if existing {
		summary.OrganizationStatus = "existing"
	} else {
		summary.OrganizationStatus = "created"
	}
	if err := r.client.LinkOrganization(ctx, r.options.ServiceDeskID, organization.ID); err != nil {
		return summary, fmt.Errorf("link Jira organization to service desk: %w", err)
	}

	members, err := r.client.ListOrganizationUsers(ctx, organization.ID)
	if err != nil {
		return summary, fmt.Errorf("list Jira organization members: %w", err)
	}
	memberEmails := make(map[string]struct{}, len(members))
	memberAccountIDs := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member.AccountID != "" {
			memberAccountIDs[member.AccountID] = struct{}{}
		}
		if email, ok := filter.NormalizeEmail(member.Email); ok {
			memberEmails[email] = struct{}{}
		}
	}
	users = normalizeDesiredUsers(users)

	var failures []string
	for _, user := range users {
		email, ok := filter.NormalizeEmail(user.Email)
		if !ok {
			// The module normally filters this already; keeping the guard here
			// protects the Jira boundary from malformed external input.
			continue
		}
		if _, ok := memberEmails[email]; ok {
			summary.CustomersExisting++
			r.logger.Debug("customer already belongs to organization", "email", email)
			continue
		}

		user.Email = email
		customer, customerExisting, ensureErr := r.client.EnsureCustomer(ctx, r.options.ServiceDeskID, user)
		if ensureErr != nil {
			failures = append(failures, fmt.Sprintf("ensure customer: %v", ensureErr))
			continue
		}
		if _, ok := memberAccountIDs[customer.AccountID]; ok {
			summary.CustomersExisting++
			r.logger.Debug("customer already belongs to organization", "email", email)
			memberEmails[email] = struct{}{}
			continue
		}
		if customerExisting {
			summary.CustomersExisting++
		} else {
			summary.CustomersCreated++
		}
		if err := r.client.AddUserToOrganization(ctx, organization.ID, customer.AccountID); err != nil {
			failures = append(failures, fmt.Sprintf("add organization membership: %v", err))
			continue
		}
		summary.MembershipsAdded++
		memberEmails[email] = struct{}{}
		memberAccountIDs[customer.AccountID] = struct{}{}
		r.logger.Debug("customer added to organization", "email", email)
	}

	if len(failures) > 0 {
		return summary, fmt.Errorf("%d Jira operations failed: %s", len(failures), strings.Join(failures, "; "))
	}
	return summary, nil
}

func normalizeDesiredUsers(users []model.CustomerUser) []model.CustomerUser {
	seen := make(map[string]struct{}, len(users))
	result := make([]model.CustomerUser, 0, len(users))
	for _, user := range users {
		email, ok := filter.NormalizeEmail(user.Email)
		if !ok || alreadySeen(seen, email) {
			continue
		}
		user.Email = email
		user.DisplayName = strings.TrimSpace(user.DisplayName)
		if user.DisplayName == "" {
			user.DisplayName = email
		}
		result = append(result, user)
	}
	return result
}

func alreadySeen(seen map[string]struct{}, email string) bool {
	if _, exists := seen[email]; exists {
		return true
	}
	seen[email] = struct{}{}
	return false
}
