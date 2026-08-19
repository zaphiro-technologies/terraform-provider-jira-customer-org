---
page_title: "Provider Jira Customer Organization - Terraform Registry"
subcategory: ""
description: |-
  Reconciles externally supplied users into a Jira Service Management customer organization.
  User discovery is intentionally handled by another Terraform provider or module.
---

# Jira Customer Organization Provider

This provider manages additive membership reconciliation for Jira Service
Management customer organizations. It does not discover users from an identity
provider; callers provide normalized email and display-name values.

## Authentication

The provider reads credentials from the process environment or injected files:

- `JIRA_BASE_URL`
- `JIRA_USER_EMAIL`
- `JIRA_API_TOKEN`
- `JIRA_USER_EMAIL_FILE`
- `JIRA_API_TOKEN_FILE`

See the `jira_customer_organization_sync` resource for configuration details.
