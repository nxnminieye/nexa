package source

import (
	"sort"

	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const (
	CapabilityID      = "source.bundle"
	CapabilityVersion = "v1.0.0"
)

type Options struct {
	Version     string
	Cache       *release.DirectoryCache
	CacheLimits release.CacheLimits
	LockLimits  lock.Limits
	TreeLimits  sourceplugin.TreeLimits
	MergeDriver engine.MergeDriver
	Executor    engine.Executor
	GoToolchain engine.GoToolchain
}

type adapter struct {
	engine             *engine.Engine
	releases           []release.Ref
	releaseInspections []sourceReleaseInspection
}

func New(options Options, providers ...sourceplugin.Provider) (plugin.Plugin, error) {
	snapshots := make([]sourceplugin.Provider, len(providers))
	refs := make([]release.Ref, len(providers))
	inspections := make([]sourceReleaseInspection, len(providers))
	for index, provider := range providers {
		snapshot, err := sourceplugin.SnapshotProvider(provider)
		if err != nil {
			return nil, err
		}
		ref, err := release.FromProvider(snapshot)
		if err != nil {
			return nil, err
		}
		profiles := snapshot.Manifest().Profiles()
		profileIDs := make([]string, len(profiles))
		for profileIndex, profile := range profiles {
			profileIDs[profileIndex] = profile.ID()
		}
		sort.Strings(profileIDs)
		snapshots[index], refs[index] = snapshot, ref
		inspections[index] = sourceReleaseInspection{
			ProviderID: ref.ProviderID(), ModulePath: ref.ModulePath(), PackagePath: ref.PackagePath(), Version: ref.Version(),
			ManifestDigest: ref.ManifestDigest().String(), TreeDigest: ref.TreeDigest().String(), Profiles: profileIDs,
		}
	}
	sort.Slice(inspections, func(left, right int) bool { return inspections[left].key() < inspections[right].key() })
	resolver, err := release.NewExactResolver(options.Cache, snapshots...)
	if err != nil {
		return nil, err
	}
	core, err := engine.New(engine.Options{
		Resolver: resolver, CacheLimits: options.CacheLimits, TreeLimits: options.TreeLimits, LockLimits: options.LockLimits,
		MergeDriver: options.MergeDriver, Executor: options.Executor, GoToolchain: options.GoToolchain,
	})
	if err != nil {
		return nil, err
	}
	owner := &adapter{
		engine: core, releases: append([]release.Ref(nil), refs...),
		releaseInspections: append([]sourceReleaseInspection(nil), inspections...),
	}
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID: "source", Version: options.Version, ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{{ID: CapabilityID, Version: CapabilityVersion}},
		},
		Commands: owner.commands(),
	})
}
