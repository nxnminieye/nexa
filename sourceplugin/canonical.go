package sourceplugin

import "encoding/json"

type canonicalManifest struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Identity   canonicalIdentity  `json:"identity"`
	Files      []canonicalFile    `json:"files"`
	Profiles   []canonicalProfile `json:"profiles"`
}

type canonicalIdentity struct {
	ProviderID  string `json:"providerId"`
	ModulePath  string `json:"modulePath"`
	PackagePath string `json:"packagePath"`
	Version     string `json:"version"`
}

type canonicalFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	Mode   string `json:"mode"`
}

type canonicalProfile struct {
	ID               string                       `json:"id"`
	Files            []string                     `json:"files"`
	RequiresProfiles []string                     `json:"requiresProfiles"`
	RequiresBundles  []canonicalBundleRequirement `json:"requiresBundles"`
	Validations      []canonicalValidationRecipe  `json:"validations"`
}

type canonicalBundleRequirement struct {
	ProviderID     string `json:"providerId"`
	ModulePath     string `json:"modulePath"`
	PackagePath    string `json:"packagePath"`
	Version        string `json:"version"`
	ProfileID      string `json:"profileId"`
	ManifestDigest string `json:"manifestDigest"`
	TreeDigest     string `json:"treeDigest"`
}

type canonicalValidationRecipe struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	WorkingDirectory string   `json:"workingDirectory"`
	Packages         []string `json:"packages"`
}

func canonicalManifestJSON(manifest Manifest) ([]byte, error) {
	document := canonicalManifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Identity: canonicalIdentity{
			ProviderID: manifest.identity.providerID, ModulePath: manifest.identity.modulePath,
			PackagePath: manifest.identity.packagePath, Version: manifest.identity.version,
		},
		Files:    make([]canonicalFile, len(manifest.files)),
		Profiles: make([]canonicalProfile, len(manifest.profiles)),
	}
	for index, file := range manifest.files {
		document.Files[index] = canonicalFile{Path: file.path, Size: file.size, Digest: file.digest.String(), Mode: string(file.mode)}
	}
	for index, profile := range manifest.profiles {
		canonical := canonicalProfile{
			ID: profile.id, Files: append([]string{}, profile.filePaths...),
			RequiresProfiles: append([]string{}, profile.requiredProfiles...),
			RequiresBundles:  make([]canonicalBundleRequirement, len(profile.requirements)),
			Validations:      make([]canonicalValidationRecipe, len(profile.validations)),
		}
		for requirementIndex, requirement := range profile.requirements {
			canonical.RequiresBundles[requirementIndex] = canonicalBundleRequirement{
				ProviderID: requirement.providerID, ModulePath: requirement.modulePath, PackagePath: requirement.packagePath,
				Version: requirement.version, ProfileID: requirement.profileID,
				ManifestDigest: requirement.manifestDigest.String(), TreeDigest: requirement.treeDigest.String(),
			}
		}
		for validationIndex, recipe := range profile.validations {
			canonical.Validations[validationIndex] = canonicalValidationRecipe{
				ID: recipe.id, Kind: string(recipe.kind), WorkingDirectory: recipe.workingDirectory,
				Packages: append([]string(nil), recipe.packages...),
			}
		}
		document.Profiles[index] = canonical
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
