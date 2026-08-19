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

const (
	MembershipModeAdditive      = "additive"
	MembershipModeAuthoritative = "authoritative"
)

type Summary struct {
	OrganizationName   string
	OrganizationStatus string
	CustomersExisting  int
	CustomersCreated   int
	MembershipsAdded   int
	MembershipsRemoved int
	CustomersRemoved   int
}

type Reconciler struct {
	client  jira.Client
	logger  *slog.Logger
	options Options
}

type membershipState struct {
	memberEmails      map[string]struct{}
	memberAccountIDs  map[string]struct{}
	desiredEmails     map[string]struct{}
	desiredAccountIDs map[string]struct{}
}

type userReconcileSummary struct {
	customersExisting int
	customersCreated  int
	membershipsAdded  int
}

func New(client jira.Client, logger *slog.Logger, options Options) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{client: client, logger: logger, options: options}
}

func (r *Reconciler) Sync(ctx context.Context, users []model.CustomerUser) (Summary, error) {
	summary := Summary{OrganizationName: r.options.OrganizationName}
	if r.options.MembershipMode != MembershipModeAdditive && r.options.MembershipMode != MembershipModeAuthoritative {
		return summary, fmt.Errorf("unsupported membership mode %q", r.options.MembershipMode)
	}
	authoritative := r.options.MembershipMode == MembershipModeAuthoritative
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
	users = normalizeDesiredUsers(users)
	state := newMembershipState(members, users)
	userSummary, failures := r.reconcileUsers(ctx, organization.ID, users, authoritative, &state)
	summary.CustomersExisting += userSummary.customersExisting
	summary.CustomersCreated += userSummary.customersCreated
	summary.MembershipsAdded += userSummary.membershipsAdded

	if authoritative && len(failures) == 0 {
		membershipsRemoved, customersRemoved, removalFailures := r.removeStaleMembers(ctx, organization.ID, r.options.ServiceDeskID, members, state.desiredEmails, state.desiredAccountIDs)
		summary.MembershipsRemoved += membershipsRemoved
		summary.CustomersRemoved += customersRemoved
		failures = append(failures, removalFailures...)
	}

	if len(failures) > 0 {
		return summary, fmt.Errorf("%d Jira operations failed: %s", len(failures), strings.Join(failures, "; "))
	}
	return summary, nil
}

func newMembershipState(members []jira.Customer, users []model.CustomerUser) membershipState {
	state := membershipState{
		memberEmails:      make(map[string]struct{}, len(members)),
		memberAccountIDs:  make(map[string]struct{}, len(members)),
		desiredEmails:     make(map[string]struct{}, len(users)),
		desiredAccountIDs: make(map[string]struct{}, len(users)),
	}
	for _, member := range members {
		if member.AccountID != "" {
			state.memberAccountIDs[member.AccountID] = struct{}{}
		}
		if email, ok := filter.NormalizeEmail(member.Email); ok {
			state.memberEmails[email] = struct{}{}
		}
	}
	for _, user := range users {
		state.desiredEmails[user.Email] = struct{}{}
	}
	return state
}

func (r *Reconciler) reconcileUsers(ctx context.Context, organizationID string, users []model.CustomerUser, authoritative bool, state *membershipState) (userReconcileSummary, []string) {
	var summary userReconcileSummary
	var failures []string
	for _, user := range users {
		userSummary, err := r.reconcileUser(ctx, organizationID, user, authoritative, state)
		summary.customersExisting += userSummary.customersExisting
		summary.customersCreated += userSummary.customersCreated
		summary.membershipsAdded += userSummary.membershipsAdded
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	return summary, failures
}

func (r *Reconciler) reconcileUser(ctx context.Context, organizationID string, user model.CustomerUser, authoritative bool, state *membershipState) (userReconcileSummary, error) {
	var summary userReconcileSummary
	email, ok := filter.NormalizeEmail(user.Email)
	if !ok {
		return summary, nil
	}
	if !authoritative {
		if _, ok := state.memberEmails[email]; ok {
			summary.customersExisting++
			r.logger.Debug("customer already belongs to organization", "email", email)
			return summary, nil
		}
	}

	user.Email = email
	customer, customerExisting, err := r.client.EnsureCustomer(ctx, r.options.ServiceDeskID, user)
	if err != nil {
		return summary, fmt.Errorf("ensure customer: %v", err)
	}
	if customer.AccountID == "" {
		return summary, fmt.Errorf("ensure customer: customer %q has no account ID", email)
	}
	state.desiredAccountIDs[customer.AccountID] = struct{}{}
	if _, ok := state.memberAccountIDs[customer.AccountID]; ok {
		summary.customersExisting++
		r.logger.Debug("customer already belongs to organization", "email", email)
		state.memberEmails[email] = struct{}{}
		return summary, nil
	}
	if customerExisting {
		summary.customersExisting++
	} else {
		summary.customersCreated++
	}
	if err := r.client.AddUserToOrganization(ctx, organizationID, customer.AccountID); err != nil {
		return summary, fmt.Errorf("add organization membership: %v", err)
	}
	summary.membershipsAdded++
	state.memberEmails[email] = struct{}{}
	state.memberAccountIDs[customer.AccountID] = struct{}{}
	r.logger.Debug("customer added to organization", "email", email)
	return summary, nil
}

func (r *Reconciler) removeStaleMembers(ctx context.Context, organizationID, serviceDeskID string, members []jira.Customer, desiredEmails, desiredAccountIDs map[string]struct{}) (int, int, []string) {
	membershipsRemoved := 0
	customersRemoved := 0
	var failures []string
	for _, member := range members {
		if desiredMember(member, desiredEmails, desiredAccountIDs) {
			continue
		}
		if member.AccountID == "" {
			failures = append(failures, fmt.Sprintf("remove organization membership: member %q has no account ID", member.Email))
			continue
		}
		if err := r.client.RemoveUserFromOrganization(ctx, organizationID, member.AccountID); err != nil {
			failures = append(failures, fmt.Sprintf("remove organization membership: %v", err))
			continue
		}
		membershipsRemoved++
		r.logger.Debug("customer removed from organization", "account_id", member.AccountID)

		organizations, err := r.client.ListUserOrganizations(ctx, member.AccountID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("list customer organizations: %v", err))
			continue
		}
		if hasOtherOrganization(organizations, organizationID) {
			continue
		}
		if err := r.client.RemoveCustomerFromServiceDesk(ctx, serviceDeskID, member.AccountID); err != nil {
			failures = append(failures, fmt.Sprintf("remove customer from service desk: %v", err))
			continue
		}
		customersRemoved++
	}
	return membershipsRemoved, customersRemoved, failures
}

func hasOtherOrganization(organizations []jira.Organization, organizationID string) bool {
	for _, organization := range organizations {
		if organization.ID != organizationID {
			return true
		}
	}
	return false
}

func desiredMember(member jira.Customer, desiredEmails, desiredAccountIDs map[string]struct{}) bool {
	if _, ok := desiredAccountIDs[member.AccountID]; ok && member.AccountID != "" {
		return true
	}
	email, ok := filter.NormalizeEmail(member.Email)
	if !ok {
		return false
	}
	_, ok = desiredEmails[email]
	return ok
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
