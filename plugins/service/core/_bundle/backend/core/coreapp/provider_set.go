package coreapp

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
)

type providerEntry struct {
	descriptor ProviderDescriptor
	provider   IdentityProvider
}

type ProviderSet struct {
	entries map[string]providerEntry
}

func NewProviderSet(providers ...IdentityProvider) (ProviderSet, error) {
	entries := make(map[string]providerEntry, len(providers))
	for _, provider := range providers {
		if interfaceNil(provider) {
			return ProviderSet{}, invalid("provider-set.new")
		}
		descriptor := provider.Descriptor()
		descriptor.ID = strings.TrimSpace(descriptor.ID)
		descriptor.Protocol = strings.TrimSpace(descriptor.Protocol)
		if descriptor.ID == "" || descriptor.Protocol == "" {
			return ProviderSet{}, invalid("provider-set.new")
		}
		if _, exists := entries[descriptor.ID]; exists {
			return ProviderSet{}, coreError("provider-set.new", CodeConflict, nil)
		}
		entries[descriptor.ID] = providerEntry{descriptor: descriptor, provider: provider}
	}
	return ProviderSet{entries: entries}, nil
}

func (s ProviderSet) Descriptors() []ProviderDescriptor {
	result := make([]ProviderDescriptor, 0, len(s.entries))
	for _, entry := range s.entries {
		result = append(result, entry.descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s ProviderSet) Authorize(ctx context.Context, providerID string, input AuthorizeInput) (AuthorizeResult, error) {
	const operation = "provider.authorize"
	entry, ok := s.entries[providerID]
	if !ok {
		return AuthorizeResult{}, coreError(operation, CodeCapabilityUnavailable, nil)
	}
	if err := ctx.Err(); err != nil {
		return AuthorizeResult{}, canceled(operation, err)
	}
	result, err := entry.provider.Authorize(ctx, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AuthorizeResult{}, canceled(operation, err)
		}
		return AuthorizeResult{}, coreError(operation, CodeProviderFailure, err)
	}
	return result, nil
}

func (s ProviderSet) Exchange(ctx context.Context, providerID string, input ExchangeInput) (NormalizedIdentity, error) {
	const operation = "provider.exchange"
	entry, ok := s.entries[providerID]
	if !ok {
		return NormalizedIdentity{}, coreError(operation, CodeCapabilityUnavailable, nil)
	}
	if err := ctx.Err(); err != nil {
		return NormalizedIdentity{}, canceled(operation, err)
	}
	identity, err := entry.provider.Exchange(ctx, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return NormalizedIdentity{}, canceled(operation, err)
		}
		return NormalizedIdentity{}, coreError(operation, CodeProviderFailure, err)
	}
	identity = cloneIdentity(identity)
	identity.SourceCode = strings.TrimSpace(identity.SourceCode)
	identity.ExternalSubject = strings.TrimSpace(identity.ExternalSubject)
	if identity.SourceCode != entry.descriptor.ID || identity.ExternalSubject == "" {
		return NormalizedIdentity{}, coreError(operation, CodeProviderFailure, nil)
	}
	return identity, nil
}

func cloneIdentity(value NormalizedIdentity) NormalizedIdentity {
	value.CandidateSubjects = append([]string(nil), value.CandidateSubjects...)
	value.ExternalGroups = append([]string(nil), value.ExternalGroups...)
	return value
}

func interfaceNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
