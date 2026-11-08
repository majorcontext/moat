package deps

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/deps/support"
)

//go:embed support/catalog.json
var supportData []byte

var releaseSupport = func() support.Catalog {
	var catalog support.Catalog
	if err := json.Unmarshal(supportData, &catalog); err != nil {
		panic(fmt.Sprintf("invalid support catalog: %v", err))
	}
	if err := catalog.Validate(); err != nil {
		panic(fmt.Sprintf("invalid support catalog: %v", err))
	}
	return catalog
}()

func validateSupport(name, version string) error {
	return releaseSupport.Check(name, version, time.Now().UTC())
}

// VersionListLabel distinguishes lifecycle policy from projects that do not
// publish support dates. A list of releases alone never proves EOL status.
func VersionListLabel(name string) string {
	if product, ok := releaseSupport.Dependencies[name]; ok && product.PublishedOnly {
		return "Recent releases"
	}
	return "Maintained lines"
}
