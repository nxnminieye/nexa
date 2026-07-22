package governance

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const skillReportVersion = "nexa.dev/governance-skill-report/v1"

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// SkillSummary identifies one validated skill relative to the selected root.
type SkillSummary struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SkillReport is the deterministic success result of skill validation.
type SkillReport struct {
	APIVersion string         `json:"apiVersion"`
	Skills     []SkillSummary `json:"skills"`
}

type skillManifest struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ValidateSkill validates either one skill directory or an immediate skills root.
func ValidateSkill(root string) error {
	_, issues := inspectSkills(root)
	if len(issues) == 0 {
		return nil
	}
	return validationError(
		"skill_manifest_invalid",
		"skill validation failed",
		"fix the reported skill manifest issues",
		issues,
	)
}

func inspectSkills(root string) (SkillReport, []Issue) {
	report := SkillReport{APIVersion: skillReportVersion, Skills: []SkillSummary{}}
	info, err := os.Lstat(root)
	if err != nil {
		return report, []Issue{issue("skill_root_missing", ".", "", "skill root is unavailable")}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return report, []Issue{issue("skill_path_unsafe", ".", "", "skill root must not be a symbolic link")}
	}
	if !info.IsDir() {
		return report, []Issue{issue("skill_root_not_directory", ".", "", "skill root must be a directory")}
	}

	manifestPath := filepath.Join(root, "SKILL.md")
	manifestInfo, manifestErr := os.Lstat(manifestPath)
	switch {
	case manifestErr == nil:
		if manifestInfo.Mode()&os.ModeSymlink != 0 {
			return report, []Issue{issue("skill_path_unsafe", ".", "SKILL.md", "skill manifest must not be a symbolic link")}
		}
		if !manifestInfo.Mode().IsRegular() {
			return report, []Issue{issue("skill_manifest_missing", ".", "SKILL.md", "SKILL.md must be a regular file")}
		}
		summary, issues := inspectSkillDirectory(root, ".")
		if len(issues) != 0 {
			return report, issues
		}
		report.Skills = append(report.Skills, summary)
		return report, nil
	case !errors.Is(manifestErr, os.ErrNotExist):
		return report, []Issue{issue("skill_manifest_unreadable", ".", "SKILL.md", "skill manifest is unavailable")}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return report, []Issue{issue("skill_root_unreadable", ".", "", "skill root cannot be read")}
	}
	var issues []Issue
	selected := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			target, targetErr := os.Stat(filepath.Join(root, entry.Name()))
			if targetErr == nil && target.IsDir() {
				issues = append(issues, issue("skill_path_unsafe", entry.Name(), "", "skill selection must not use a symbolic link"))
			}
			continue
		}
		if !entry.IsDir() {
			continue
		}
		selected++
		summary, skillIssues := inspectSkillDirectory(filepath.Join(root, entry.Name()), entry.Name())
		issues = append(issues, skillIssues...)
		if len(skillIssues) == 0 {
			report.Skills = append(report.Skills, summary)
		}
	}
	if selected == 0 && len(issues) == 0 {
		issues = append(issues, issue("skill_manifest_missing", ".", "SKILL.md", "no skill manifests were selected"))
	}
	sort.Slice(report.Skills, func(i, j int) bool {
		if report.Skills[i].Name != report.Skills[j].Name {
			return report.Skills[i].Name < report.Skills[j].Name
		}
		return report.Skills[i].Path < report.Skills[j].Path
	})
	return report, issues
}

func inspectSkillDirectory(root, reportPath string) (SkillSummary, []Issue) {
	summary := SkillSummary{Path: filepath.ToSlash(reportPath)}
	manifestPath := filepath.Join(root, "SKILL.md")
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return summary, []Issue{issue("skill_manifest_missing", summary.Path, "SKILL.md", "SKILL.md is required")}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return summary, []Issue{issue("skill_path_unsafe", summary.Path, "SKILL.md", "skill manifest must not be a symbolic link")}
	}
	if !info.Mode().IsRegular() {
		return summary, []Issue{issue("skill_manifest_missing", summary.Path, "SKILL.md", "SKILL.md must be a regular file")}
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return summary, []Issue{issue("skill_manifest_unreadable", summary.Path, "SKILL.md", "skill manifest cannot be read")}
	}
	manifest, parseIssue := parseSkillManifest(contents, summary.Path)
	if parseIssue != nil {
		return summary, []Issue{*parseIssue}
	}

	summary.Name = strings.TrimSpace(manifest.Name)
	var issues []Issue
	if summary.Name == "" {
		issues = append(issues, issue("skill_name_missing", summary.Path, "name", "skill name is required"))
	} else if !skillNamePattern.MatchString(summary.Name) {
		issues = append(issues, issue("skill_name_invalid", summary.Path, "name", "skill name must use lower-hyphen form"))
	}
	if strings.TrimSpace(manifest.Description) == "" {
		issues = append(issues, issue("skill_description_missing", summary.Path, "description", "skill description is required"))
	}
	if summary.Name != "" && summary.Name != filepath.Base(root) {
		issues = append(issues, issue("skill_name_mismatch", summary.Path, "name", "skill name must match its directory"))
	}
	return summary, issues
}

func parseSkillManifest(contents []byte, object string) (skillManifest, *Issue) {
	text := strings.ReplaceAll(string(contents), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		invalid := issue("skill_frontmatter_invalid", object, "SKILL.md", "skill manifest must start with YAML frontmatter")
		return skillManifest{}, &invalid
	}
	closing := -1
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			closing = index
			break
		}
	}
	if closing < 0 {
		invalid := issue("skill_frontmatter_invalid", object, "SKILL.md", "skill YAML frontmatter is not closed")
		return skillManifest{}, &invalid
	}

	decoder := yaml.NewDecoder(strings.NewReader(strings.Join(lines[1:closing], "\n")))
	var manifest skillManifest
	if err := decoder.Decode(&manifest); err != nil {
		invalid := issue("skill_frontmatter_invalid", object, "SKILL.md", "skill YAML frontmatter is invalid")
		return skillManifest{}, &invalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		invalid := issue("skill_frontmatter_invalid", object, "SKILL.md", "skill YAML frontmatter must contain one document")
		return skillManifest{}, &invalid
	}
	return manifest, nil
}

func issue(code, object, field, message string) Issue {
	return Issue{Code: code, Object: object, Field: field, Message: message}
}
