mock_provider "google" {}

variables {
  billing_account      = "000000-000000-000000"
  budget_amount_units  = "1"
  notification_channel = "projects/test/notificationChannels/test"
}

run "publisher_identity" {
  command = plan

  assert {
    condition = google_iam_workload_identity_pool_provider.pusaq_github.attribute_condition == join(" && ", [
      "assertion.repository_id == '${var.github_repository_id}'",
      "assertion.repository_owner_id == '${var.github_owner_id}'",
      "assertion.ref == 'refs/heads/main'",
      "assertion.event_name == 'push'",
      "assertion.workflow_ref == '${var.github_repo}/.github/workflows/ci.yml@refs/heads/main'",
      "assertion.job_workflow_ref == '${var.github_repo}/.github/workflows/release-image.yml@refs/heads/main'",
    ])
    error_message = "Federation must restrict every publishing identity claim."
  }

  assert {
    condition     = endswith(google_service_account_iam_member.gha_wif.member, "/attribute.repository_id/${var.github_repository_id}")
    error_message = "The impersonation binding must use the immutable repository ID."
  }

  assert {
    condition     = google_artifact_registry_repository_iam_member.gha_writer.role == "roles/artifactregistry.writer"
    error_message = "The publisher must have only registry write access."
  }
}
