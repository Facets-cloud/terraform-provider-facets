variable "subscription_id" {
  type        = string
  description = "Azure subscription ID owning the target resources"
}

variable "tenant_id" {
  type        = string
  description = "Microsoft Entra ID tenant ID"
}

variable "client_id" {
  type        = string
  description = "Application (client) ID of the service principal / managed identity"
}
