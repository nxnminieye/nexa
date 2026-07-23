package governance

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	frameworkskills "github.com/nxnminieye/nexa/skills"
)

const (
	skillSyncResultVersion = "nexa.dev/governance-skill-sync-result/v1"
	skillSyncTarget        = ".codex/skills"
)

type skillSyncResult struct {
	APIVersion string   `json:"apiVersion"`
	Target     string   `json:"target"`
	Skills     []string `json:"skills"`
	FileCount  int      `json:"fileCount"`
}

type embeddedSkillAsset struct {
	path     string
	contents []byte
	mode     fs.FileMode
}

type embeddedSkillSet struct {
	names  []string
	assets []embeddedSkillAsset
}

func syncSkills(repoRoot string) (skillSyncResult, error) {
	result := skillSyncResult{
		APIVersion: skillSyncResultVersion,
		Target:     skillSyncTarget,
		Skills:     []string{},
	}
	assets, err := loadEmbeddedSkillSet()
	if err != nil {
		return result, syncError("skill_source_invalid", "embedded Nexa skills are invalid")
	}
	result.Skills = append(result.Skills, assets.names...)
	result.FileCount = len(assets.assets)

	if !filepath.IsAbs(repoRoot) {
		return result, syncError("skill_repo_root_invalid", "repository root must be an existing absolute directory")
	}
	rootInfo, err := os.Lstat(repoRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return result, syncError("skill_repo_root_invalid", "repository root must be an existing absolute directory")
	}
	repository, err := os.OpenRoot(filepath.Clean(repoRoot))
	if err != nil {
		return result, syncError("skill_repo_root_invalid", "repository root must be an existing absolute directory")
	}
	defer repository.Close()

	managedNames, err := preflightSkillTarget(repository, assets.names)
	if err != nil {
		return result, syncError("skill_target_unsafe", "skill target contains an unsafe filesystem entry")
	}
	if err := repository.MkdirAll(skillSyncTarget, 0o755); err != nil {
		return result, syncError("skill_target_unwritable", "skill target cannot be created")
	}

	assetsBySkill := make(map[string][]embeddedSkillAsset, len(assets.names))
	for _, asset := range assets.assets {
		name := strings.SplitN(asset.path, "/", 2)[0]
		assetsBySkill[name] = append(assetsBySkill[name], asset)
	}
	for _, name := range managedNames {
		target := path.Join(skillSyncTarget, name)
		if err := repository.RemoveAll(target); err != nil {
			return result, syncError("skill_target_unwritable", "managed skill directory cannot be replaced")
		}
	}
	for _, name := range assets.names {
		target := path.Join(skillSyncTarget, name)
		if err := repository.MkdirAll(target, 0o755); err != nil {
			return result, syncError("skill_target_unwritable", "managed skill directory cannot be created")
		}
		for _, asset := range assetsBySkill[name] {
			destination := path.Join(skillSyncTarget, asset.path)
			if err := repository.MkdirAll(path.Dir(destination), 0o755); err != nil {
				return result, syncError("skill_target_unwritable", "managed skill parent directory cannot be created")
			}
			if err := repository.WriteFile(destination, asset.contents, asset.mode.Perm()); err != nil {
				return result, syncError("skill_target_unwritable", "managed skill file cannot be written")
			}
		}
	}

	for _, name := range assets.names {
		if err := ValidateSkill(filepath.Join(repoRoot, filepath.FromSlash(skillSyncTarget), name)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func loadEmbeddedSkillSet() (embeddedSkillSet, error) {
	set := embeddedSkillSet{names: []string{}, assets: []embeddedSkillAsset{}}
	entries, err := fs.ReadDir(frameworkskills.Files(), ".")
	if err != nil {
		return set, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nexa-") || !skillNamePattern.MatchString(entry.Name()) {
			return set, errors.New("unexpected embedded skill root")
		}
		name := entry.Name()
		set.names = append(set.names, name)
		hasManifest := false
		err := fs.WalkDir(frameworkskills.Files(), name, func(assetPath string, child fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path.Clean(assetPath) != assetPath || path.IsAbs(assetPath) || strings.HasPrefix(assetPath, "../") {
				return errors.New("unsafe embedded asset path")
			}
			if child.IsDir() {
				return nil
			}
			if !child.Type().IsRegular() {
				return errors.New("embedded asset is not a regular file")
			}
			contents, err := fs.ReadFile(frameworkskills.Files(), assetPath)
			if err != nil {
				return err
			}
			if assetPath == path.Join(name, "SKILL.md") {
				hasManifest = true
			}
			set.assets = append(set.assets, embeddedSkillAsset{path: assetPath, contents: contents, mode: 0o644})
			return nil
		})
		if err != nil {
			return set, err
		}
		if !hasManifest {
			return set, errors.New("embedded skill manifest is missing")
		}
	}
	if len(set.names) == 0 || len(set.assets) == 0 {
		return set, errors.New("embedded skill set is empty")
	}
	sort.Strings(set.names)
	sort.Slice(set.assets, func(i, j int) bool { return set.assets[i].path < set.assets[j].path })
	return set, nil
}

func preflightSkillTarget(repository *os.Root, embeddedNames []string) ([]string, error) {
	for _, parent := range []string{".codex", skillSyncTarget} {
		info, err := repository.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe skill target parent")
		}
	}
	managedSet := make(map[string]struct{}, len(embeddedNames))
	for _, name := range embeddedNames {
		managedSet[name] = struct{}{}
	}
	entries, err := fs.ReadDir(repository.FS(), skillSyncTarget)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "nexa-") {
			managedSet[entry.Name()] = struct{}{}
		}
	}
	managedNames := make([]string, 0, len(managedSet))
	for name := range managedSet {
		managedNames = append(managedNames, name)
	}
	sort.Strings(managedNames)
	for _, name := range managedNames {
		target := path.Join(skillSyncTarget, name)
		info, err := repository.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe managed skill directory")
		}
		managed, err := repository.OpenRoot(target)
		if err != nil {
			return nil, err
		}
		walkErr := fs.WalkDir(managed.FS(), ".", func(_ string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type().IsRegular() {
				return nil
			}
			return errors.New("unsafe managed skill entry")
		})
		closeErr := managed.Close()
		if walkErr != nil {
			return nil, walkErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return managedNames, nil
}

func syncError(issueCode, message string) error {
	return validationError(
		"skill_sync_failed",
		"skill synchronization failed",
		"fix the repository or embedded skill assets and retry",
		[]Issue{issue(issueCode, skillSyncTarget, "", message)},
	)
}
