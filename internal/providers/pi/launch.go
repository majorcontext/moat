package pi

// Pi requires --model alongside --provider. Resolve an omitted model inside
// the container, after extensions and their catalogs are available, using Pi's
// public model listing. Its rows begin with provider and model ID and are sorted
// by ID within each provider. Match the provider column exactly: the listing's
// optional search is fuzzy and can also return other providers.
//
// Do not use --models <provider>/** as a substitute: Pi falls back to another
// authenticated provider when that scope matches no models. An empty or failed
// listing must stop startup instead. The remaining arguments stay positional,
// preserving prompts and model IDs without evaluating them as shell code.
const defaultModelLaunchScript = `provider="$1"
shift
models=$(NO_COLOR=1 pi --list-models "$provider") || exit "$?"
model=$(printf '%s\n' "$models" | awk -v provider="$provider" '$1 == provider && NF >= 2 {print $2; exit}')
if [ -z "$model" ]; then
  echo "moat: no available Pi models for provider '$provider'; check its grant and model catalog, or set --model / pi.model explicitly" >&2
  exit 1
fi
exec pi --provider "$provider" --model "$model" "$@"
`
