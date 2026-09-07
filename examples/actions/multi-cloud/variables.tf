variable "environment_unique_name" {
  type        = string
  description = "Unique name of the Facets environment"
}

variable "instance_name" {
  type        = string
  description = "Blueprint resource name the actions attach to"
}

variable "db_instance_id" {
  type        = string
  description = "RDS instance identifier"
}

variable "resource_group_name" {
  type        = string
  description = "Azure resource group holding the Flexible Server"
}

variable "server_name" {
  type        = string
  description = "Azure PostgreSQL Flexible Server name"
}

variable "namespace" {
  type        = string
  description = "Namespace of the deployment to restart"
}

variable "deployment_name" {
  type        = string
  description = "Deployment to restart"
}
