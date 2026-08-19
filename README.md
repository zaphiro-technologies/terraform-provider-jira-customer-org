# Terraform Provider Jira Customer Organization

This provider reconciles externally supplied users into a Jira Service
Management customer organization.

It deliberately contains only Jira logic. User discovery belongs to the calling
Terraform module and can use AzureAD, Keycloak, or another provider. The
provider receives a normalized list of `{ email, display_name }` values; it does
not call Microsoft Graph or any other identity API.

## Supported behavior

For each reconciliation, the provider:

1. Finds or creates the configured Jira Service Management organization.
2. Ensures that organization is linked to the configured service desk.
3. Reuses existing Jira customers and Jira accounts where possible.
4. Creates missing customers and adds them to the organization.
5. In `authoritative` mode, removes organization members that are not in the
  supplied user list.
6. In `authoritative` mode, scans all service-desk customers after membership
  reconciliation and removes customers that no longer belong to any
  organization.

In `additive` mode, existing organization members are left untouched. The
`membership_mode` argument controls which behavior is used.

`authoritative` mode is destructive: use it only when this resource owns the
organization membership. A customer that still belongs to another Jira
organization is retained.

Jira service-desk open access must be disabled for authoritative orphan
customer cleanup. Jira rejects customer deletion while open access is enabled.

The resource is safe to run repeatedly. The input user list is write-only and is
not stored in Terraform state.

When the resource is destroyed, it removes the organization's users from the
service desk when they do not belong to another organization, removes the
organization memberships, and deletes the Jira organization. Destruction is
destructive and requires Jira service-desk open access to be disabled.

## Provider configuration

The provider has no Terraform configuration arguments. Jira credentials are read
at runtime from:

| Environment variable | Description                                                |
| -------------------- | ---------------------------------------------------------- |
| `JIRA_BASE_URL`      | Jira Cloud site URL; the resource `base_url` must match it |
| `JIRA_USER_EMAIL`    | Jira account email used for API authentication             |
| `JIRA_API_TOKEN`     | Jira API token                                             |

`JIRA_USER_EMAIL_FILE` and `JIRA_API_TOKEN_FILE` can be used for
Crossplane-injected secret files. Credentials and authorization headers are
never logged.

## Resource example

```hcl
terraform {
  required_version = ">= 1.11"

  required_providers {
    jira = {
      source  = "zaphiro-technologies/jira-customer-org"
      version = "~> 0.0.1"
    }
  }
}

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

`sync_trigger` is an operator-controlled value. Change it when the upstream
directory data changes and Terraform must execute the resource again. Do not use
a timestamp or another value that changes on every refresh.

Set `membership_mode = "additive"` to preserve existing organization members,
or `membership_mode = "authoritative"` to make the organization match
`users_wo` and clean up orphaned customers.

## Crossplane

The provider is downloaded by Terraform during `terraform init` and can be
cached by `provider-terraform`. The Crossplane provider image therefore does not
need a Go runtime or a helper binary. It needs network access to the Terraform
Registry (or a configured provider mirror) and Jira, plus the Jira credential
environment variables injected by Kubernetes.

The source-specific wrapper module remains in the
[tf-modules](https://github.com/zaphiro-technologies/tf-modules) repository. It
converts identity-provider results into the input contract used here.

## Development

```shell
go test ./...
go build -trimpath -ldflags='-s -w -X main.version=0.0.1' \
  -o terraform-provider-jira-customer-org .
```

For local Terraform development, use a CLI development override and point it to
the built binary. Remove that override before testing registry installation.

## Release

Releases are created by `.github/workflows/release.yml` when a semantic-version
tag such as `v0.0.2` is pushed. GoReleaser creates platform archives, SHA-256
checksums, the Terraform Registry manifest, and a detached GPG signature.

Before the first release:

1. Generate an RSA GPG signing key.
2. Add its armored public key to the Terraform Registry provider publishing
   settings.
3. Add `GPG_PRIVATE_KEY` and `PASSPHRASE` as GitHub Actions secrets.
4. Commit the provider and push a tag such as `v0.0.2`.
5. Enable the provider in the Terraform Registry after the GitHub release is
   finalized.
