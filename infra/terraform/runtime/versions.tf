terraform {
  required_version = ">= 1.9.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 6.0.0, < 8.0.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.6.0, < 4.0.0"
    }
  }

  # Same shared state bucket as the foundation, own prefix. The bucket name
  # is passed at init time (`make runtime-init`).
  backend "gcs" {
    prefix = "pusaq/runtime"
  }
}
