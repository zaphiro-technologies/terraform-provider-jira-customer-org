---
page_title:
  "jira_customer_organization_sync Resource - Jira Customer Organization"
subcategory: ""
description: |-
  Ensures externally supplied users exist as Jira Service Management customers and belong to an organization.
---

# jira_customer_organization_sync (Resource)

Ensures that a Jira Service Management organization exists and that the supplied
customers belong to it. With `additive` membership, existing customers and
organization members are reused. With `authoritative` membership, members not
in `users_wo` are removed. After membership reconciliation, all service-desk
customers are checked and those with no remaining organization are removed
from the service desk.

The `users_wo` argument is write-only and is not persisted in Terraform state.

When this resource is destroyed, users belonging only to this organization are
removed from the service desk, all users are removed from this organization's
membership, and the organization is deleted. Users belonging to another Jira
organization are retained. Jira service-desk open access must be disabled for
the user cleanup to succeed.

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
- `membership_mode` (String, Required) - `additive` preserves existing
  organization members. `authoritative` removes members not in `users_wo` and
  removes customers that no longer belong to any organization.
- `sync_trigger` (String, Required) - Stable operator-controlled value used to
  request a new reconciliation.

In `authoritative` mode, Jira service-desk open access must be disabled before
orphan customers can be removed. Jira rejects customer deletion while open
access is enabled.
