resource "google_artifact_registry_repository" "pusaq" {
  project       = var.project_id
  location      = var.region
  repository_id = var.artifact_repo_id
  description   = "Pusaq gateway and mock provider images"
  format        = "DOCKER"

  # Untagged layers (superseded manifests) go after a week.
  cleanup_policies {
    id     = "untagged-7d"
    action = "DELETE"
    condition {
      tag_state  = "UNTAGGED"
      older_than = "604800s"
    }
  }

  # Tagged images older than 30 days are deleted, except the 10 most recent
  # (KEEP wins over DELETE). Without a DELETE rule for tagged images the KEEP
  # rule would protect nothing.
  cleanup_policies {
    id     = "older-than-30d"
    action = "DELETE"
    condition {
      tag_state  = "ANY"
      older_than = "2592000s"
    }
  }

  cleanup_policies {
    id     = "keep-last-10-tagged"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }

  depends_on = [google_project_service.enabled]
}
