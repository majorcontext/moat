// Package gcloud implements a credential provider for Google Cloud.
//
// Unlike header-injection providers (GitHub, Claude), gcloud follows the
// AWS model: the host daemon mints short-lived access tokens using the
// host's Application Default Credentials and serves them to the container
// via a GCE metadata server emulator. The container's HTTP_PROXY routes
// requests to metadata.google.internal through the proxy, which serves
// them from the per-run metadata handler. No long-lived credentials enter
// the container.
package gcloud
