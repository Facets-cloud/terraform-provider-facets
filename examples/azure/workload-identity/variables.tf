variable "subscription_id" {
  type        = string
  description = "Azure subscription ID the action operates against"
}

variable "tenant_id" {
  type        = string
  description = "Azure AD tenant ID"
}

variable "client_id" {
  type        = string
  description = "Application (client) ID of the federated service principal"
}

variable "instance_name" {
  type        = string
  description = "Blueprint resource name this action attaches to"
}

variable "environment_unique_name" {
  type        = string
  description = "Unique name of the Facets environment"
}

variable "resource_group_name" {
  type        = string
  description = "Resource group holding the PostgreSQL Flexible Server"
}

variable "server_name" {
  type        = string
  description = "Name of the PostgreSQL Flexible Server"
}
