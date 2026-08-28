# Azure DB start/stop actions.
#
# Credentials are configured once here, on the provider. The provider creates and
# maintains the Kubernetes Secret holding the password and wires the action to read
# it via secretKeyRef, so:
#
#   - the user clicking the action in the Facets UI supplies nothing
#   - the password appears in neither Terraform state nor the Tekton manifests
#   - no Secret has to be created by hand, and no name has to be agreed on
#
# In a Facets blueprint these four values come from the cloud_account module's
# output type, so this block is generated rather than written.

terraform {
  required_providers {
    facets = {
      source = "Facets-cloud/facets"
    }
  }
}

provider "facets" {
  azure = {
    subscription_id = var.subscription_id
    tenant_id       = var.tenant_id
    client_id       = var.client_id
    client_secret   = var.client_secret
  }
}

resource "facets_tekton_action_azure" "stop_database" {
  name                 = "Stop Database"
  description          = "Stops the PostgreSQL Flexible Server without destroying it"
  facets_resource_name = "my-db"
  facets_environment   = { unique_name = "dev" }
  facets_resource = {
    kind    = "postgres"
    flavor  = "fk-tg-ported"
    version = "1.0"
    spec    = {}
  }

  steps = [{
    name  = "stop"
    image = "mcr.microsoft.com/azure-cli:2.61.0"
    # Already authenticated: the prepended setup-credentials step ran az login.
    script = <<-EOT
      #!/bin/bash
      set -e
      az postgres flexible-server stop \
        --resource-group my-rg --name my-db-server
    EOT
  }]
}

variable "subscription_id" { type = string }
variable "tenant_id" { type = string }
variable "client_id" { type = string }
variable "client_secret" {
  type      = string
  sensitive = true
}
