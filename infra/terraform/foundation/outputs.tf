output "workload_identity_provider" {
  description = "Value for GitHub Actions `workload_identity_provider` (repo variable GCP_WORKLOAD_IDENTITY_PROVIDER)."
  value       = google_iam_workload_identity_pool_provider.pusaq_github.name
}

output "service_account_email" {
  description = "Value for GitHub Actions `service_account` (repo variable GCP_SERVICE_ACCOUNT)."
  value       = google_service_account.gha.email
}

output "artifact_registry_url" {
  description = "Docker registry path images are pushed to."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.pusaq.repository_id}"
}

output "github_actions_auth_snippet" {
  description = "Auth step for a workflow, with the values filled in."
  value       = <<-EOT
    - uses: google-github-actions/auth@v2
      with:
        workload_identity_provider: ${google_iam_workload_identity_pool_provider.pusaq_github.name}
        service_account: ${google_service_account.gha.email}
  EOT
}
