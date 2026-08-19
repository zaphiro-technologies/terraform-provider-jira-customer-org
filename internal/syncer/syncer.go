package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/jira"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/reconcile"
)

type Config struct {
	OrganizationName string
	ServiceDeskID    string
	BaseURL          string
	Users            []model.CustomerUser
	MembershipMode   string
}

type Result struct {
	UsersReceived    int
	UsersReconciled  int
	ReconcileSummary reconcile.Summary
}

// Run performs Jira reconciliation for users supplied by an external source
// provider. Source discovery is deliberately outside this provider; if a
// Terraform data source fails, Terraform does not invoke this resource.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) (Result, error) {
	if logger == nil {
		logger = newLogger()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	summary, err := jiraSyncer{
		baseURL: cfg.BaseURL,
		logger:  logger,
		options: reconcile.Options{
			OrganizationName: cfg.OrganizationName,
			ServiceDeskID:    cfg.ServiceDeskID,
			MembershipMode:   cfg.MembershipMode,
		},
	}.Sync(ctx, cfg.Users)

	result := Result{
		ReconcileSummary: summary,
		UsersReceived:    len(cfg.Users),
		UsersReconciled:  summary.CustomersExisting + summary.CustomersCreated,
	}
	logSummary(logger, result)
	if err != nil {
		return result, fmt.Errorf("jira reconciliation failed: %w", err)
	}
	return result, nil
}

type jiraSyncer struct {
	baseURL string
	logger  *slog.Logger
	options reconcile.Options
}

func (s jiraSyncer) Sync(ctx context.Context, users []model.CustomerUser) (reconcile.Summary, error) {
	jiraClient, err := jira.NewClientFromEnvWithBaseURL(http.DefaultClient, s.baseURL)
	if err != nil {
		return reconcile.Summary{}, fmt.Errorf("configure Jira client: %w", err)
	}
	return reconcile.New(jiraClient, s.logger, s.options).Sync(ctx, users)
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("JCS_LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func logSummary(logger *slog.Logger, result Result) {
	summary := result.ReconcileSummary
	logger.Info("Jira customer reconciliation complete",
		"organization", summary.OrganizationName,
		"organization_status", summary.OrganizationStatus,
		"users_received", result.UsersReceived,
		"users_reconciled", result.UsersReconciled,
		"customers_existing", summary.CustomersExisting,
		"customers_created", summary.CustomersCreated,
		"memberships_added", summary.MembershipsAdded,
		"memberships_removed", 0,
	)
}
