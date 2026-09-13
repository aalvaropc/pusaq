provider "google" {
  project = var.project_id
  region  = var.region

  # The budgets API bills its quota to the caller's project; pin it.
  user_project_override = true
  billing_project       = var.project_id

  # Applied to every resource that supports labels. The label-filtered budget
  # in budget.tf keys on `project = "pusaq"`, so anything Terraform creates
  # for Pusaq later (Cloud Run, Memorystore) is counted automatically.
  default_labels = var.labels
}
