# Example terraform.tfvars file
# Copy this to terraform.tfvars and fill in your values

aws_region = "ap-south-1"

# IAM role to assume (cross-account or same-account)
# The pod must have IAM permissions (via IRSA, instance profile, etc.) to assume this role
role_arn     = "arn:aws:iam::338360549315:role/facets-cluster-role-FacetsCPClusterRole-13KJ7V4OY2AOC"
session_name = "facets-worfklows-session"
external_id  = "a92dab86-62ae-4506-8e69-b8e4aee94431"  # Optional: only if required by role trust policy

# Facets configuration
resource_name    = "my-application"
environment_name = "production"
