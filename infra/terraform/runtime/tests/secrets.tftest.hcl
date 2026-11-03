mock_provider "google" {
  mock_resource "google_service_account" {
    defaults = {
      email  = "pusaq-gateway@test.iam.gserviceaccount.com"
      member = "serviceAccount:pusaq-gateway@test.iam.gserviceaccount.com"
    }
  }
}

mock_provider "random" {}

variables {
  image_tag = "0123456789abcdef0123456789abcdef01234567"
  api_keys  = "test-key:tenant"
}

run "gateway_credentials" {
  command = apply

  assert {
    condition     = contains(keys(google_project_service.enabled), "secretmanager.googleapis.com")
    error_message = "The runtime must enable the Secret Manager API."
  }

  assert {
    condition     = alltrue([for svc in google_cloud_run_v2_service.gateway : svc.template[0].service_account == google_service_account.gateway.email])
    error_message = "Every gateway must run as the dedicated gateway service account."
  }

  assert {
    condition = alltrue(flatten([
      for svc in google_cloud_run_v2_service.gateway : [
        length([for e in svc.template[0].containers[0].env : e if contains(["PUSAQ_API_KEYS", "PUSAQ_ADMIN_TOKEN"], e.name)]) == 2,
        [
          for e in svc.template[0].containers[0].env :
          e.value == null && e.value_source[0].secret_key_ref[0].secret == google_secret_manager_secret.gateway[e.name == "PUSAQ_API_KEYS" ? "api-keys" : "admin-token"].secret_id
          if contains(["PUSAQ_API_KEYS", "PUSAQ_ADMIN_TOKEN"], e.name)
        ],
      ]
    ]))
    error_message = "API keys and the admin token must reach Cloud Run as secret references, never as plain values."
  }

  assert {
    condition = alltrue([
      for svc in google_cloud_run_v2_service.gateway : alltrue([
        for e in svc.template[0].containers[0].env :
        e.value_source[0].secret_key_ref[0].version == (e.name == "PUSAQ_API_KEYS" ? google_secret_manager_secret_version.api_keys.version : google_secret_manager_secret_version.admin_token.version)
        if contains(["PUSAQ_API_KEYS", "PUSAQ_ADMIN_TOKEN"], e.name)
      ])
    ])
    error_message = "Gateways must pin the secret versions the module writes."
  }

  assert {
    condition = length(google_secret_manager_secret_iam_member.gateway) == 2 && alltrue([
      for name, binding in google_secret_manager_secret_iam_member.gateway :
      binding.secret_id == google_secret_manager_secret.gateway[name].id &&
      binding.role == "roles/secretmanager.secretAccessor" &&
      binding.member == google_service_account.gateway.member
    ])
    error_message = "Only the gateway service account may read the two gateway secrets, with accessor rights only."
  }

  assert {
    condition     = google_secret_manager_secret_version.api_keys.secret_data == null
    error_message = "API keys must be written write-only so they never reach state."
  }
}
