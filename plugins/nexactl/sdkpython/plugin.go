package sdkpython

import (
	"context"
	"reflect"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

const (
	pluginID          = "sdk-python-assets"
	pluginVersion     = "v0.1.0"
	capabilityID      = "generation.sdk-python-assets"
	capabilityVersion = "v1.0.0"
)

type Options struct {
	WheelBuilder sdkpythonassets.WheelBuilder
}

type adapter struct {
	owner    assetOwner
	hasBuild bool
	schemas  schemaSet
}

type assetOwner interface {
	Write(context.Context, sdkpythonassets.WriteRequest) (sdkpythonassets.WriteResult, error)
	Check(context.Context, sdkpythonassets.CheckRequest) (sdkpythonassets.CheckResult, error)
	Build(context.Context, sdkpythonassets.BuildRequest) (sdkpythonassets.BuildResult, error)
}

func New(options Options) (plugin.Plugin, error) {
	bundle, err := sdkpythonassets.NewAssetBundle()
	if err != nil {
		return nil, err
	}
	schemas, err := newSchemaSet(bundle.Roles())
	if err != nil {
		return nil, err
	}
	builder := options.WheelBuilder
	hasBuild := !nilWheelBuilder(builder)
	if !hasBuild {
		builder = nil
	}
	owner := sdkpythonassets.NewOwner(builder)
	return newWithOwnerSchemas(owner, hasBuild, schemas)
}

func nilWheelBuilder(builder sdkpythonassets.WheelBuilder) bool {
	if builder == nil {
		return true
	}
	value := reflect.ValueOf(builder)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func newWithOwner(owner assetOwner, hasBuild bool) (plugin.Plugin, error) {
	bundle, err := sdkpythonassets.NewAssetBundle()
	if err != nil {
		return nil, err
	}
	schemas, err := newSchemaSet(bundle.Roles())
	if err != nil {
		return nil, err
	}
	return newWithOwnerSchemas(owner, hasBuild, schemas)
}

func newWithOwnerSchemas(owner assetOwner, hasBuild bool, schemas schemaSet) (plugin.Plugin, error) {
	pluginAdapter := &adapter{owner: owner, hasBuild: hasBuild, schemas: schemas}
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              pluginID,
			Version:         pluginVersion,
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{{
				ID:      capabilityID,
				Version: capabilityVersion,
			}},
		},
		Commands: pluginAdapter.commands(),
	})
}
