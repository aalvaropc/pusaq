# Monthly budget over everything labeled project=pusaq in the project. The
# alerts go to an existing notification channel; nothing here creates one.
resource "google_billing_budget" "pusaq" {
  billing_account = var.billing_account
  display_name    = "pusaq monthly (label project=pusaq)"

  budget_filter {
    projects = ["projects/${data.google_project.current.number}"]
    labels = {
      project = "pusaq"
    }
    # Measure gross spend: credits are applied after cost, and a budget that
    # only sees net spend stays at zero until the credit is gone.
    credit_types_treatment = "EXCLUDE_ALL_CREDITS"
    calendar_period        = "MONTH"
  }

  amount {
    specified_amount {
      currency_code = "USD"
      units         = var.budget_amount_units
    }
  }

  threshold_rules {
    threshold_percent = 0.5
  }
  threshold_rules {
    threshold_percent = 0.75
  }
  threshold_rules {
    threshold_percent = 0.9
  }
  threshold_rules {
    threshold_percent = 1.0
  }

  all_updates_rule {
    monitoring_notification_channels = [var.notification_channel]
    disable_default_iam_recipients   = false
  }

  depends_on = [google_project_service.enabled]
}
