variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "role_arn" {
  description = "ARN of the IAM role to assume. The pod must have permissions to assume this role."
  type        = string
  # Example: "arn:aws:iam::123456789012:role/my-cross-account-role"
}

variable "session_name" {
  description = "Session name for the assumed role session (used for CloudTrail auditing)"
  type        = string
  default     = "terraform-facets-session"
}

variable "external_id" {
  description = "External ID for assuming the role (optional, required if role trust policy specifies it)"
  type        = string
  default     = ""
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
