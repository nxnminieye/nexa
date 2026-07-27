package coreapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type PermissionCatalogEntry struct {
	Code        string
	Description string
}

type MenuCatalogEntry struct {
	Code        string
	ParentCode  string
	DisplayName string
	Path        string
}

type CatalogSyncInput struct {
	SourceID    string
	Digest      string
	Permissions []PermissionCatalogEntry
	Menus       []MenuCatalogEntry
}

type CatalogSyncStoreInput struct {
	SourceID     string
	Digest       string
	Permissions  []PermissionCatalogEntry
	Menus        []MenuCatalogEntry
	DisableStale bool
}

type CatalogSyncResult struct {
	SourceID            string
	Digest              string
	PermissionsUpserted int
	PermissionsDisabled int
	MenusUpserted       int
	MenusDisabled       int
}

type CatalogService struct {
	store      CatalogStore
	reconciler PolicyReconciler
}

func NewCatalogService(store CatalogStore, reconciler PolicyReconciler) (*CatalogService, error) {
	if interfaceNil(store) || interfaceNil(reconciler) {
		return nil, invalid("catalog.new")
	}
	return &CatalogService{store: store, reconciler: reconciler}, nil
}

func (s *CatalogService) Sync(ctx context.Context, input CatalogSyncInput) (CatalogSyncResult, error) {
	const operation = "catalog.sync"
	if err := contextError(operation, ctx); err != nil {
		return CatalogSyncResult{}, err
	}
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.Digest = strings.TrimSpace(input.Digest)
	if input.SourceID == "" || input.Digest == "" {
		return CatalogSyncResult{}, invalid(operation)
	}
	permissions, err := canonicalPermissions(input.Permissions)
	if err != nil {
		return CatalogSyncResult{}, invalid(operation)
	}
	menus, err := canonicalMenus(input.Menus)
	if err != nil {
		return CatalogSyncResult{}, invalid(operation)
	}
	digest, err := canonicalCatalogDigest(permissions, menus)
	if err != nil || input.Digest != digest {
		return CatalogSyncResult{}, invalid(operation)
	}
	result, err := s.store.SyncCatalog(ctx, CatalogSyncStoreInput{
		SourceID: input.SourceID, Digest: input.Digest, Permissions: permissions, Menus: menus, DisableStale: true,
	})
	if err != nil {
		return CatalogSyncResult{}, mapIAMStoreError(operation, err)
	}
	if result.SourceID != input.SourceID || result.Digest != input.Digest {
		return CatalogSyncResult{}, coreError(operation, CodeFailedPrecondition, nil)
	}
	if err := s.reconciler.ReconcilePolicy(ctx, PolicyReconcileInput{Kind: PolicyResourceCatalog, ResourceID: input.SourceID}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CatalogSyncResult{}, canceled(operation, err)
		}
		return CatalogSyncResult{}, coreError(operation, CodeCapabilityUnavailable, err)
	}
	return result, nil
}

// CanonicalCatalogDigest returns the content identity accepted by CatalogService.Sync.
func CanonicalCatalogDigest(permissions []PermissionCatalogEntry, menus []MenuCatalogEntry) (string, error) {
	canonicalPermissionValues, err := canonicalPermissions(permissions)
	if err != nil {
		return "", err
	}
	canonicalMenuValues, err := canonicalMenus(menus)
	if err != nil {
		return "", err
	}
	return canonicalCatalogDigest(canonicalPermissionValues, canonicalMenuValues)
}

func canonicalCatalogDigest(permissions []PermissionCatalogEntry, menus []MenuCatalogEntry) (string, error) {
	payload, err := json.Marshal(struct {
		Permissions []PermissionCatalogEntry `json:"permissions"`
		Menus       []MenuCatalogEntry       `json:"menus"`
	}{Permissions: permissions, Menus: menus})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalPermissions(values []PermissionCatalogEntry) ([]PermissionCatalogEntry, error) {
	byCode := make(map[string]PermissionCatalogEntry, len(values))
	for _, value := range values {
		value.Code = strings.TrimSpace(value.Code)
		value.Description = strings.TrimSpace(value.Description)
		if !validCode(value.Code) {
			return nil, ErrStoreFailedPrecondition
		}
		if previous, exists := byCode[value.Code]; exists && previous != value {
			return nil, ErrStoreConflict
		}
		byCode[value.Code] = value
	}
	result := make([]PermissionCatalogEntry, 0, len(byCode))
	for _, value := range byCode {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result, nil
}

func canonicalMenus(values []MenuCatalogEntry) ([]MenuCatalogEntry, error) {
	byCode := make(map[string]MenuCatalogEntry, len(values))
	for _, value := range values {
		value.Code = strings.TrimSpace(value.Code)
		value.ParentCode = strings.TrimSpace(value.ParentCode)
		value.DisplayName = strings.TrimSpace(value.DisplayName)
		value.Path = strings.TrimSpace(value.Path)
		if !validCode(value.Code) || value.ParentCode != "" && !validCode(value.ParentCode) || value.DisplayName == "" {
			return nil, ErrStoreFailedPrecondition
		}
		if previous, exists := byCode[value.Code]; exists && previous != value {
			return nil, ErrStoreConflict
		}
		byCode[value.Code] = value
	}
	result := make([]MenuCatalogEntry, 0, len(byCode))
	for _, value := range byCode {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result, nil
}
