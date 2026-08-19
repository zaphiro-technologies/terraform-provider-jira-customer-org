---
page_title:
  "jira_customer_organization_sync Resource - Jira Customer Organization"
subcategory: ""
description: |-
  Ensures externally supplied users exist as Jira Service Management customers and belong to an organization.
---

# jira_customer_organization_sync (Resource)

Ensures that a Jira Service Management organization exists and that the supplied
customers belong to it. Reconciliation is additive-only. Existing customers and
organization members are reused; customers are never deleted and organization
members are never removed.

The `users_wo` argument is write-only and is not persisted in Terraform state.

## Example Usage

```hcl
resource "jira_customer_organization_sync" "this" {
  organization_name = "Acme Energy"
  service_desk_id   = "SUP"
  base_url          = "https://example.atlassian.net"

  users_wo = [
    {
      email        = "user@example.com"
      display_name = "Example User"
    }
  ]

  membership_mode = "additive"
  sync_trigger    = "source-revision-1"
}
```

## Argument Reference

- `organization_name` (String, Required) - Jira Service Management organization
  name to find or create.
- `service_desk_id` (String, Required) - Jira Service Management service desk ID
  or project key.
- `base_url` (String, Required) - HTTPS Jira Cloud site URL without a path.
- `users_wo` (List of Objects, Required, Write-Only) - Desired users. Each
  object has required `email` and optional `display_name`.
- `membership_mode` (String, Required) - Must be `additive` in version 1.
- `sync_trigger` (String, Required) - Stable operator-controlled value used to
  request a new reconciliation.
