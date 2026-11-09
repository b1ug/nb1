// Package doctor reports build metadata and existing daemon snapshots. It never
// enumerates or opens USB devices, acquires device locks, or submits work.
package doctor

import (
	"runtime"
	"runtime/debug"
)

type Module struct {
	Path             string `json:"path"`
	RequestedVersion string `json:"requested_version"`
	Version          string `json:"version"`
	Source           string `json:"source"`
}

type Build struct {
	InfoAvailable      bool     `json:"info_available"`
	GoVersion          string   `json:"go_version"`
	OS                 string   `json:"os"`
	Arch               string   `json:"arch"`
	CGOEnabled         bool     `json:"cgo_enabled"`
	NativeHIDSupported bool     `json:"native_hid_supported"`
	Main               Module   `json:"main"`
	Revision           string   `json:"revision"`
	Modified           string   `json:"modified"`
	Dependencies       []Module `json:"dependencies"`
}

// ReadBuild reports the modules embedded in this executable, not go.mod from
// the current directory. Local replacement paths and arbitrary build settings
// are deliberately omitted. nativeHIDSupported must be a non-I/O build check.
func ReadBuild(nativeHIDSupported bool) Build {
	info, ok := debug.ReadBuildInfo()
	return buildFromInfo(info, ok, nativeHIDSupported)
}

func buildFromInfo(info *debug.BuildInfo, ok, supported bool) Build {
	build := Build{InfoAvailable: ok && info != nil, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		CGOEnabled: compiledWithCGO, NativeHIDSupported: supported, Main: Module{Path: "github.com/b1ug/nb1", Source: "unavailable"},
		Modified: "unknown", Dependencies: []Module{moduleInfo(nil, "github.com/b1ug/blink1-go"), moduleInfo(nil, "github.com/b1ug/gid")}}
	if !build.InfoAvailable {
		return build
	}
	build.GoVersion = info.GoVersion
	build.Main = moduleInfo(&info.Main, info.Main.Path)
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			build.Revision = setting.Value
		case "vcs.modified":
			build.Modified = setting.Value
		}
	}
	for i, dependency := range build.Dependencies {
		for _, module := range info.Deps {
			if module.Path == dependency.Path {
				build.Dependencies[i] = moduleInfo(module, dependency.Path)
				break
			}
		}
	}
	return build
}

func moduleInfo(module *debug.Module, path string) Module {
	result := Module{Path: path, Source: "unavailable"}
	if module == nil {
		return result
	}
	result.RequestedVersion, result.Version, result.Source = module.Version, module.Version, "module"
	if module.Replace != nil {
		result.Version, result.Source = module.Replace.Version, "replacement"
		if result.Version == "" {
			// A workspace checkout has no reproducible module version. Do not
			// misrepresent the requested version as the code actually built.
			result.Source = "local_unversioned"
		}
	}
	return result
}
