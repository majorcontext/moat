---
title: "Google Cloud"
navTitle: "Google Cloud"
description: "Use gcloud CLI and Google Cloud client libraries inside a Moat sandbox."
keywords: ["moat", "gcloud", "google cloud", "gcp", "credentials", "metadata server"]
---

# Google Cloud

Moat injects Google Cloud credentials into containers through a GCE metadata server emulator. The gcloud CLI, `google-cloud-*` client libraries, and any tool that uses Application Default Credentials (ADC) work without configuration changes inside the container.

No long-lived credentials (refresh tokens, service account keys) enter the container.

## Prerequisites

Configure Application Default Credentials on the host:

```bash
gcloud auth application-default login
```

Or set `GOOGLE_APPLICATION_CREDENTIALS` to a service account JSON key file.

## Grant a credential

```bash
moat grant gcloud --project my-project
```

The `--project` flag sets the GCP project ID for API calls. If omitted, Moat checks `GOOGLE_CLOUD_PROJECT`, `CLOUDSDK_CORE_PROJECT`, then `gcloud config get-value project`.

### Service account impersonation

```bash
moat grant gcloud \
  --project my-project \
  --impersonate-service-account deploy@my-project.iam.gserviceaccount.com
```

The host's credentials are used to impersonate the specified service account via IAM. The container receives tokens scoped to the impersonated identity.

### Explicit key file

```bash
moat grant gcloud \
  --project my-project \
  --key-file /path/to/service-account.json
```

The key file stays on the host. The daemon reads it to mint access tokens — the file is never mounted into the container.

## Run with gcloud access

```bash
moat run --grant gcloud ./my-project
```

Or in `moat.yaml`:

```yaml
grants:
  - gcloud
```

## How it works

1. `moat grant gcloud` stores the project ID and credential configuration (not tokens) in the encrypted credential store
2. When a run starts, the proxy daemon creates a metadata server emulator for that run
3. The container's `HTTP_PROXY` routes requests to `metadata.google.internal` through the proxy
4. The proxy intercepts these requests and serves access tokens from the emulator
5. Access tokens are cached and refreshed 5 minutes before expiry

This is the same pattern Google Cloud uses on GCE instances and Cloud Run — the container behaves as if it is running on Google Cloud infrastructure.

## Verifying inside the container

```bash
# Check the metadata server responds
curl -s -H "Metadata-Flavor: Google" \
  http://metadata.google.internal/computeMetadata/v1/project/project-id

# Fetch an access token
curl -s -H "Metadata-Flavor: Google" \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token

# Use gcloud normally
gcloud projects list --limit=1
```

## Limitations

- **ID tokens** (`audience=...` parameter) are not supported. The metadata emulator returns 404 for identity token requests.
- **Workload Identity Federation** from non-GCP external sources works if the host's ADC file is configured for it, but there is no dedicated CLI UX.
