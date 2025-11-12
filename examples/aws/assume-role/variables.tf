variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "ap-south-1"
}

variable "role_arn" {
  description = "ARN of the IAM role to assume. The pod's IRSA role must have permissions to assume this role."
  type        = string
}

variable "external_id" {
  description = "External ID for assuming the role (optional, required if role trust policy specifies it). Provides additional security against the confused deputy problem."
  type        = string
}

variable "session_name" {
  description = "Session name for the assumed role."
  type        = string
  default     = "terraform-session"
}

variable "resource_name" {
  description = "Facets resource name"
  type        = string
  default     = "my-application"
}

variable "environment_name" {
  description = "Facets environment unique name"
  type        = string
  default     = "production"
}
