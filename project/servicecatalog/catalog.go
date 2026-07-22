package servicecatalog

import (
	"encoding/json"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

const APIVersion = "nexa.dev/service-catalog/v1"
const Kind = "ServiceCatalog"

// ServiceNodeAPIVersion identifies the canonical service topology envelope.
const ServiceNodeAPIVersion = "nexa.dev/service-node/v1"

// CapabilityBindingNodeAPIVersion identifies the canonical capability binding envelope.
const CapabilityBindingNodeAPIVersion = "nexa.dev/capability-binding-node/v1"

type Catalog struct {
	apiVersion      string
	services        []Service
	dependencyOrder []Service
	sources         []provenance.Source
	sourceIndex     map[string]int
}

type Service struct {
	id                  string
	root                string
	dependsOn           []string
	capabilityBindings  []CapabilityBinding
	source              provenance.Source
	canonicalSourceJSON []byte
}

type CapabilityBinding struct {
	id                  string
	apiVersion          string
	source              provenance.Source
	canonicalSourceJSON []byte
}

func Empty() Catalog {
	return Catalog{apiVersion: APIVersion}
}

func (c Catalog) APIVersion() string {
	return c.apiVersion
}

func (c Catalog) Len() int {
	return len(c.services)
}

func (c Catalog) Services() []Service {
	return append([]Service(nil), c.services...)
}

func (c Catalog) Lookup(id string) (Service, bool) {
	index := sort.Search(len(c.services), func(index int) bool {
		return c.services[index].id >= id
	})
	if index == len(c.services) || c.services[index].id != id {
		return Service{}, false
	}
	return c.services[index], true
}

func (c Catalog) DependencyOrder() []Service {
	return append([]Service(nil), c.dependencyOrder...)
}

// Sources returns the canonical owner-node sources projected from the catalog.
func (c Catalog) Sources() []provenance.Source {
	return append([]provenance.Source(nil), c.sources...)
}

// Source returns the source whose canonical reference exactly matches ref.
func (c Catalog) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	index, ok := c.sourceIndex[ref.String()]
	if !ok {
		return provenance.Source{}, false
	}
	return c.sources[index], true
}

func (s Service) ID() string {
	return s.id
}

func (s Service) Root() string {
	return s.root
}

func (s Service) DependsOn() []string {
	return append([]string(nil), s.dependsOn...)
}

func (s Service) CapabilityBindings() []CapabilityBinding {
	return append([]CapabilityBinding(nil), s.capabilityBindings...)
}

// Source returns the canonical source of this service topology node.
func (s Service) Source() provenance.Source {
	return s.source
}

// CanonicalSourceJSON returns the no-newline JCS bytes hashed by Source.
func (s Service) CanonicalSourceJSON() []byte {
	return append([]byte(nil), s.canonicalSourceJSON...)
}

func (b CapabilityBinding) ID() string {
	return b.id
}

func (b CapabilityBinding) APIVersion() string {
	return b.apiVersion
}

// Source returns the canonical source of this capability binding node.
func (b CapabilityBinding) Source() provenance.Source {
	return b.source
}

// CanonicalSourceJSON returns the no-newline JCS bytes hashed by Source.
func (b CapabilityBinding) CanonicalSourceJSON() []byte {
	return append([]byte(nil), b.canonicalSourceJSON...)
}

func catalogFromDocument(sourcePath string, document catalogDocument) (Catalog, error) {
	services := make([]Service, len(document.Services))
	sources := make([]provenance.Source, 0, len(document.Services)*2)
	for index, rawService := range document.Services {
		dependsOn := append([]string(nil), rawService.DependsOn...)
		sort.Strings(dependsOn)
		serviceID := stringValue(rawService.ID)
		serviceRoot := stringValue(rawService.Root)
		serviceSource, serviceCanonical, err := serviceNodeSource(sourcePath, serviceID, serviceRoot, dependsOn)
		if err != nil {
			return Catalog{}, err
		}
		sources = append(sources, serviceSource)
		bindings := make([]CapabilityBinding, len(rawService.CapabilityBindings))
		for bindingIndex, binding := range rawService.CapabilityBindings {
			bindingID := stringValue(binding.ID)
			bindingVersion := stringValue(binding.APIVersion)
			bindingSource, bindingCanonical, err := capabilityBindingNodeSource(sourcePath, serviceID, bindingID, bindingVersion)
			if err != nil {
				return Catalog{}, err
			}
			bindings[bindingIndex] = CapabilityBinding{
				id: bindingID, apiVersion: bindingVersion, source: bindingSource, canonicalSourceJSON: bindingCanonical,
			}
			sources = append(sources, bindingSource)
		}
		sort.Slice(bindings, func(left, right int) bool {
			if bindings[left].id == bindings[right].id {
				return bindings[left].apiVersion < bindings[right].apiVersion
			}
			return bindings[left].id < bindings[right].id
		})
		services[index] = Service{
			id:                  serviceID,
			root:                serviceRoot,
			dependsOn:           dependsOn,
			capabilityBindings:  bindings,
			source:              serviceSource,
			canonicalSourceJSON: serviceCanonical,
		}
	}
	sort.Slice(services, func(left, right int) bool { return services[left].id < services[right].id })
	sort.Slice(sources, func(left, right int) bool { return sources[left].Ref.String() < sources[right].Ref.String() })
	sourceIndex := make(map[string]int, len(sources))
	for index, source := range sources {
		sourceIndex[source.Ref.String()] = index
	}
	return Catalog{
		apiVersion:      stringValue(document.APIVersion),
		services:        services,
		dependencyOrder: dependencyOrder(services),
		sources:         sources,
		sourceIndex:     sourceIndex,
	}, nil
}

func serviceNodeSource(sourcePath, id, root string, dependsOn []string) (provenance.Source, []byte, error) {
	ref, err := provenance.RepositoryRef(sourcePath, "service:"+id)
	if err != nil {
		return provenance.Source{}, nil, err
	}
	canonical, err := json.Marshal(struct {
		APIVersion string   `json:"apiVersion"`
		ID         string   `json:"id"`
		Root       string   `json:"root"`
		DependsOn  []string `json:"dependsOn"`
	}{APIVersion: ServiceNodeAPIVersion, ID: id, Root: root, DependsOn: append([]string{}, dependsOn...)})
	if err != nil {
		return provenance.Source{}, nil, err
	}
	canonical, err = jcs.Transform(canonical)
	if err != nil {
		return provenance.Source{}, nil, err
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256(canonical)}, canonical, nil
}

func capabilityBindingNodeSource(sourcePath, serviceID, id, apiVersion string) (provenance.Source, []byte, error) {
	fragment := "service:" + serviceID + "/binding:" + id + "@" + apiVersion
	ref, err := provenance.RepositoryRef(sourcePath, fragment)
	if err != nil {
		return provenance.Source{}, nil, err
	}
	canonical, err := json.Marshal(struct {
		APIVersion           string `json:"apiVersion"`
		ServiceID            string `json:"serviceId"`
		ID                   string `json:"id"`
		CapabilityAPIVersion string `json:"capabilityApiVersion"`
	}{APIVersion: CapabilityBindingNodeAPIVersion, ServiceID: serviceID, ID: id, CapabilityAPIVersion: apiVersion})
	if err != nil {
		return provenance.Source{}, nil, err
	}
	canonical, err = jcs.Transform(canonical)
	if err != nil {
		return provenance.Source{}, nil, err
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256(canonical)}, canonical, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func dependencyOrder(services []Service) []Service {
	byID := make(map[string]Service, len(services))
	indegree := make(map[string]int, len(services))
	dependents := make(map[string][]string, len(services))
	for _, service := range services {
		byID[service.id] = service
		indegree[service.id] = 0
	}
	for _, service := range services {
		for _, dependency := range service.dependsOn {
			if _, exists := byID[dependency]; !exists {
				continue
			}
			indegree[service.id]++
			dependents[dependency] = append(dependents[dependency], service.id)
		}
	}
	ready := make([]string, 0, len(services))
	for _, service := range services {
		if indegree[service.id] == 0 {
			ready = append(ready, service.id)
		}
	}
	order := make([]Service, 0, len(services))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, byID[id])
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	return order
}
