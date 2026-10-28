terraform {
  required_version = ">= 1.9.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 6.0.0, < 8.8.2"
    }
  }

  # The state bucket already exists and is shared with other projects, each
  # under its own prefix. The bucket name is passed at init time
  # (`make infra-init` → -backend-config="bucket=...") so it is not hardcoded.
  backend "gcs" {
    prefix = "pusaq/foundation"
  }
}
