# GitHub Actions publishes images without long-lived keys: the workflow
# exchanges its OIDC token for short-lived credentials of a service account
# that can only write to the Pusaq registry.

locals {
  pool_name = "projects/${data.google_project.current.number}/locations/global/workloadIdentityPools/${var.workload_identity_pool_id}"
}

resource "google_service_account" "gha" {
  project      = var.project_id
  account_id   = "pusaq-gha"
  display_name = "GitHub Actions publisher (pusaq)"
  depends_on   = [google_project_service.enabled]
}

# A provider of its own in the existing pool: providers carry the attribute
# condition that pins them to one repository, so each repository gets one.
resource "google_iam_workload_identity_pool_provider" "pusaq_github" {
  project                            = var.project_id
  workload_identity_pool_id          = var.workload_identity_pool_id
  workload_identity_pool_provider_id = "pusaq-github"
  display_name                       = "GitHub OIDC (pusaq)"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }

  # Tokens from any other repository are rejected before IAM is consulted.
  attribute_condition = "assertion.repository == '${var.github_repo}'"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }

  depends_on = [google_project_service.enabled]
}

# Only workflows of the Pusaq repository may impersonate the publisher.
resource "google_service_account_iam_member" "gha_wif" {
  service_account_id = google_service_account.gha.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${local.pool_name}/attribute.repository/${var.github_repo}"
}

# Least privilege: write access to this one repository, no project roles.
resource "google_artifact_registry_repository_iam_member" "gha_writer" {
  project    = var.project_id
  location   = google_artifact_registry_repository.pusaq.location
  repository = google_artifact_registry_repository.pusaq.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.gha.email}"
}
