package run

// This file holds provider-specific container setup used by Create.

import (
	"path/filepath"
	"strings"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// setupProviderMounts collects provider-specific container mounts and init
// files for the run's grants. Init files are written to disk by moat-init.sh at
// container startup, avoiding bind mounts for config dirs that tools need to
// write to. Provider cleanup paths are recorded on r. It returns empty results
// (degrading, not failing) when there is no container home or the credential
// store can't be opened — matching the original inline behavior.
func (m *Manager) setupProviderMounts(r *Run, grants []string, containerHome string, openCredStore func() (*credential.FileStore, error)) (mounts []container.MountConfig, initFiles map[string]string) {
	initFiles = make(map[string]string)
	if containerHome == "" {
		return nil, initFiles
	}
	store, storeErr := openCredStore()
	if storeErr != nil {
		return nil, initFiles
	}
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]
		credName := credential.Provider(provider.ResolveName(grantName))
		if cred, err := store.Get(credName); err == nil {
			if prov := provider.Get(grantName); prov != nil {
				provCred := provider.FromLegacy(cred)
				providerMounts, cleanupPath, mountErr := prov.ContainerMounts(provCred, containerHome)
				if mountErr != nil {
					log.Debug("failed to set up provider mounts", "provider", credName, "error", mountErr)
				} else {
					mounts = append(mounts, providerMounts...)
					if cleanupPath != "" {
						if r.ProviderCleanupPaths == nil {
							r.ProviderCleanupPaths = make(map[string]string)
						}
						r.ProviderCleanupPaths[string(credName)] = cleanupPath
					}
				}
				// Collect init files from providers that implement InitFileProvider.
				// This runs independently of the mount step above: a ContainerMounts
				// error skips only mounts and cleanup-path recording, not init files
				// (preserved from the original inline code).
				if ifp, ok := prov.(provider.InitFileProvider); ok {
					for p, content := range ifp.ContainerInitFiles(provCred, containerHome) {
						cleaned := filepath.Clean(p)
						if !filepath.IsAbs(cleaned) || !strings.HasPrefix(cleaned, containerHome+string(filepath.Separator)) {
							log.Warn("init file path outside container home, skipping", "provider", credName, "path", p)
							continue
						}
						initFiles[cleaned] = content
					}
				}
			}
		}
	}
	return mounts, initFiles
}
