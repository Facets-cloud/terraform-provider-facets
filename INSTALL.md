# Installation Guide

## Installation Methods

### Method 1: Direct Installation from GitHub Releases (Recommended)

1. **Download the provider binary** from the [releases page](https://github.com/facets-cloud/terraform-provider-facets/releases)

2. **Extract and install** the provider:
   ```bash
   # For Linux/macOS
   PROVIDER_VERSION="0.1.0"
   OS_ARCH="$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m)"

   # Download
   wget https://github.com/facets-cloud/terraform-provider-facets/releases/download/v${PROVIDER_VERSION}/terraform-provider-facets_${PROVIDER_VERSION}_${OS_ARCH}.zip

   # Extract
   unzip terraform-provider-facets_${PROVIDER_VERSION}_${OS_ARCH}.zip

   # Install
   mkdir -p ~/.terraform.d/plugins/github.com/facets-cloud/facets/${PROVIDER_VERSION}/${OS_ARCH}
   mv terraform-provider-facets_v${PROVIDER_VERSION} ~/.terraform.d/plugins/github.com/facets-cloud/facets/${PROVIDER_VERSION}/${OS_ARCH}/terraform-provider-facets
   chmod +x ~/.terraform.d/plugins/github.com/facets-cloud/facets/${PROVIDER_VERSION}/${OS_ARCH}/terraform-provider-facets
   ```

3. **Configure Terraform** to use the provider:
   ```hcl
   terraform {
     required_providers {
       facets = {
         source  = "github.com/facets-cloud/facets"
         version = "~> 0.1.0"
       }
     }
   }
   ```

### Method 2: Install from Source (Development)

1. **Clone the repository**:
   ```bash
   git clone https://github.com/facets-cloud/terraform-provider-facets.git
   cd terraform-provider-facets
   ```

2. **Build and install**:
   ```bash
   make install
   ```

3. **Use in Terraform**:
   ```hcl
   terraform {
     required_providers {
       facets = {
         source = "github.com/facets-cloud/facets"
       }
     }
   }
   ```

### Method 3: Local Development Installation

For testing local changes:

```bash
# Build
go build -o terraform-provider-facets

# Install to local plugins directory
mkdir -p ~/.terraform.d/plugins/localhost/facets-cloud/facets/1.0.0/darwin_arm64
cp terraform-provider-facets ~/.terraform.d/plugins/localhost/facets-cloud/facets/1.0.0/darwin_arm64/
```

Use in Terraform:
```hcl
terraform {
  required_providers {
    facets = {
      source = "localhost/facets-cloud/facets"
    }
  }
}
```

## Terraform CLI Configuration

Alternatively, you can configure Terraform to use the GitHub source automatically by creating a `~/.terraformrc` file:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/home/user/.terraform.d/plugins"
    include = ["github.com/facets-cloud/*"]
  }
  direct {
    exclude = ["github.com/facets-cloud/*"]
  }
}
```

## Verification

After installation, verify the provider is recognized:

```bash
terraform init
terraform version
```

You should see:
```
+ provider github.com/facets-cloud/facets v0.1.0
```

## Upgrading

To upgrade to a new version:

1. Download the new version from releases
2. Replace the binary in the plugins directory
3. Run `terraform init -upgrade`

## Troubleshooting

### Provider not found
- Ensure the directory structure matches: `~/.terraform.d/plugins/github.com/facets-cloud/facets/VERSION/OS_ARCH/`
- Check file permissions (binary should be executable)
- Verify the source in your `required_providers` block matches the directory structure

### Checksum errors
- Delete `.terraform.lock.hcl` and run `terraform init` again
- For development, use the `localhost/` namespace to bypass checksum validation

### Version conflicts
- Check `terraform.lock.hcl` for version constraints
- Run `terraform init -upgrade` to update to the latest compatible version
