// Package gcloud implements a credential provider for Google Cloud.
//
// Unlike header-injection providers (GitHub, Claude), gcloud follows the
// AWS model: the host daemon mints short-lived access tokens using the
// host's Application Default Credentials and serves them to the container
// via a GCE metadata server emulator. The container is pointed at the
// emulator via GCE_METADATA_HOST. No long-lived credentials enter the
// container.
package gcloud
