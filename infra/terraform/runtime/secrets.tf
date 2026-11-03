resource "random_password" "admin_token" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "gateway" {
  for_each = toset(["api-keys", "admin-token"])

  secret_id = "pusaq-${each.key}"

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "api_keys" {
  secret                 = google_secret_manager_secret.gateway["api-keys"].id
  secret_data_wo         = var.api_keys
  secret_data_wo_version = var.api_keys_version

  lifecycle {
    create_before_destroy = true
  }
}

# Not write-only: the admin_token output, which chaos-run.sh reads, keeps the
# token in state anyway.
resource "google_secret_manager_secret_version" "admin_token" {
  secret      = google_secret_manager_secret.gateway["admin-token"].id
  secret_data = random_password.admin_token.result
}

resource "google_secret_manager_secret_iam_member" "gateway" {
  for_each = google_secret_manager_secret.gateway

  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.gateway.member
}
