package model

// CustomerUser is the source-independent representation consumed by the Jira
// reconciliation layer. The producer can be Entra, Keycloak, or any other
// Terraform data source; the Jira provider does not know which one.
type CustomerUser struct {
	Email       string
	DisplayName string
}
