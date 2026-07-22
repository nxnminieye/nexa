package buildinfo

import (
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type Reader interface {
	ReadBuildInfo() (*debug.BuildInfo, bool)
}

type ReaderFunc func() (*debug.BuildInfo, bool)

func (fn ReaderFunc) ReadBuildInfo() (*debug.BuildInfo, bool) { return fn() }

type Info struct {
	identity      Identity
	available     bool
	commit        string
	dirty         bool
	vcsTime       string
	goVersion     string
	modulePath    string
	moduleVersion string
}

func (i Info) APIVersion() string      { return APIVersion }
func (i Info) Identity() Identity      { return i.identity }
func (i Info) Service() string         { return i.identity.service }
func (i Info) Kind() string            { return i.identity.kind }
func (i Info) ContractVersion() string { return i.identity.contractVersion }
func (i Info) Available() bool         { return i.available }
func (i Info) Commit() string          { return i.commit }
func (i Info) Dirty() bool             { return i.dirty }
func (i Info) VCSTime() string         { return i.vcsTime }
func (i Info) GoVersion() string       { return i.goVersion }
func (i Info) ModulePath() string      { return i.modulePath }
func (i Info) ModuleVersion() string   { return i.moduleVersion }

func Resolve(identity Identity, reader Reader) (Info, error) {
	if err := validateIdentity(identity); err != nil {
		return Info{}, err
	}
	if reader == nil {
		return Info{}, invalid("reader_nil", "", "build info reader is nil")
	}
	build, ok := reader.ReadBuildInfo()
	if !ok {
		return fallback(identity), nil
	}
	if build == nil {
		return Info{}, invalid("reader_result_invalid", "", "build info reader returned an invalid result")
	}
	return resolveBuildInfo(identity, build)
}

func Current(identity Identity) (Info, error) {
	return resolveCurrent(identity, debug.ReadBuildInfo)
}

func resolveCurrent(identity Identity, read func() (*debug.BuildInfo, bool)) (Info, error) {
	return Resolve(identity, ReaderFunc(read))
}

func fallback(identity Identity) Info {
	return Info{identity: identity, commit: "unknown", dirty: true}
}

func resolveBuildInfo(identity Identity, build *debug.BuildInfo) (Info, error) {
	if !validProjectedText(build.GoVersion) {
		return Info{}, invalid("go_version_invalid", "/goVersion", "Go version is invalid")
	}
	if !validModulePath(build.Main.Path) {
		return Info{}, invalid("module_path_invalid", "/modulePath", "module path is invalid")
	}
	if !validModuleVersion(build.Main.Version) {
		return Info{}, invalid("module_version_invalid", "/moduleVersion", "module version is invalid")
	}

	info := fallback(identity)
	info.goVersion = build.GoVersion
	info.modulePath = build.Main.Path
	info.moduleVersion = build.Main.Version
	seen := make(map[string]struct{}, 3)
	revisionSeen := false
	modified := false
	modifiedSeen := false
	for index, setting := range build.Settings {
		if !relevantSetting(setting.Key) {
			continue
		}
		if _, duplicate := seen[setting.Key]; duplicate {
			return Info{}, invalid("setting_duplicate", settingPointer(index, "key"), "build setting is duplicated")
		}
		seen[setting.Key] = struct{}{}
		switch setting.Key {
		case "vcs.revision":
			if !validRevision(setting.Value) {
				return Info{}, invalid("revision_invalid", settingPointer(index, "value"), "build revision is invalid")
			}
			revisionSeen = true
			info.commit = setting.Value
		case "vcs.time":
			parsed, err := time.Parse(time.RFC3339, setting.Value)
			if err != nil {
				return Info{}, invalid("vcs_time_invalid", settingPointer(index, "value"), "build VCS time is invalid")
			}
			info.vcsTime = parsed.UTC().Format(time.RFC3339Nano)
		case "vcs.modified":
			if setting.Value != "true" && setting.Value != "false" {
				return Info{}, invalid("vcs_modified_invalid", settingPointer(index, "value"), "build modified state is invalid")
			}
			modifiedSeen = true
			modified = setting.Value == "true"
		}
	}
	if revisionSeen {
		info.available = true
		if modifiedSeen {
			info.dirty = modified
		}
	} else {
		info.available = false
		info.commit = "unknown"
		info.dirty = true
	}
	return info, nil
}

func validProjectedText(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validModuleVersion(value string) bool {
	if !validProjectedText(value) {
		return false
	}
	if value == "" || value == "(devel)" {
		return true
	}
	if !semver.IsValid(value) || !strings.HasPrefix(value, "v") {
		return false
	}
	core := strings.TrimPrefix(value, "v")
	if separator := strings.IndexAny(core, "-+"); separator >= 0 {
		core = core[:separator]
	}
	return len(strings.Split(core, ".")) == 3
}

func validModulePath(value string) bool {
	return validProjectedText(value) && (value == "" || module.CheckPath(value) == nil)
}

func validRevision(value string) bool {
	if value == "unknown" || len(value) == 0 || len(value) > 256 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '!' || value[index] > '~' {
			return false
		}
	}
	return true
}

func relevantSetting(key string) bool {
	return key == "vcs.revision" || key == "vcs.time" || key == "vcs.modified"
}

func settingPointer(index int, field string) string {
	return "/settings/" + strconv.Itoa(index) + "/" + field
}
