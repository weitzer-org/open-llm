output "gateway_url" {
  value       = google_cloud_run_v2_service.gateway.uri
  description = "The public-facing HTTP endpoint URI of the Go Gateway (load the browser for the chat console here!)"
}

output "model_bucket_name" {
  value       = google_storage_bucket.model_bucket.name
  description = "The GCS bucket designated as the Model Zoo. Upload raw model safetensors directories here."
}

output "api_auth_secret_id" {
  value       = google_secret_manager_secret.api_auth_secret.secret_id
  description = "The secret identifier for the API pre-shared access key"
}

output "secret_retrieval_command" {
  value       = "gcloud secrets versions access latest --secret=\"${google_secret_manager_secret.api_auth_secret.secret_id}\" --project=\"${var.project_id}\""
  description = "Copy-paste this terminal command to retrieve the auto-generated secure API token from Secret Manager"
}
