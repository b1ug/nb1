package systemduser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const selectedProperties = "Id,LoadState,ActiveState,SubState,FragmentPath,SourcePath,DropInPaths,Transient,NeedDaemonReload,UnitFileState,Job,NoNewPrivileges"

func showArgs() []string {
	return []string{"--user", "--no-pager", "--no-ask-password", "show", "--all", "--property=" + selectedProperties, UnitName}
}

func commandArgs(action string) []string {
	args := []string{"--user", "--no-pager", "--no-ask-password"}
	if action == "start" || action == "stop" {
		args = append(args, "--job-mode=fail")
	}
	args = append(args, action)
	if action != "daemon-reload" {
		args = append(args, UnitName)
	}
	return args
}

// show --all emits empty selected properties too. Missing, repeated, unknown,
// truncated or contradictory fields never become an absent-unit assertion.
func parseProperties(output string) (Properties, error) {
	values := make(map[string]string)
	allowed := make(map[string]bool)
	for _, name := range strings.Split(selectedProperties, ",") {
		allowed[name] = true
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || !allowed[parts[0]] {
			return Properties{}, fmt.Errorf("systemctl show returned unknown or incomplete properties")
		}
		if _, exists := values[parts[0]]; exists {
			return Properties{}, fmt.Errorf("systemctl show returned duplicate properties")
		}
		if strings.IndexFunc(parts[1], unicode.IsControl) >= 0 {
			return Properties{}, fmt.Errorf("systemctl properties contain control characters; state is unknown")
		}
		values[parts[0]] = parts[1]
	}
	missingOptionalSecurity := values["LoadState"] == "not-found"
	for name := range allowed {
		if _, exists := values[name]; !exists && !(missingOptionalSecurity && name == "NoNewPrivileges") {
			return Properties{}, fmt.Errorf("systemctl properties are missing")
		}
	}
	if values["Id"] != UnitName {
		return Properties{}, fmt.Errorf("systemctl properties are missing or the unit identity does not match")
	}
	boolean := func(name string) (bool, error) {
		switch values[name] {
		case "yes":
			return true, nil
		case "no":
			return false, nil
		default:
			return false, fmt.Errorf("a systemctl boolean property is unrecognized")
		}
	}
	transient, err := boolean("Transient")
	if err != nil {
		return Properties{}, err
	}
	reload, err := boolean("NeedDaemonReload")
	if err != nil {
		return Properties{}, err
	}
	var noNewPrivileges *bool
	if _, exists := values["NoNewPrivileges"]; exists {
		value, err := boolean("NoNewPrivileges")
		if err != nil {
			return Properties{}, err
		}
		noNewPrivileges = &value
	}
	var job uint64
	if value := values["Job"]; value != "" {
		job, err = strconv.ParseUint(value, 10, 32)
		if err != nil || strconv.FormatUint(job, 10) != value {
			return Properties{}, fmt.Errorf("the systemctl Job property is unrecognized")
		}
	}
	if values["LoadState"] == "" || values["ActiveState"] == "" || values["SubState"] == "" {
		return Properties{}, fmt.Errorf("systemctl state properties are empty")
	}
	return Properties{ID: values["Id"], LoadState: values["LoadState"], ActiveState: values["ActiveState"], SubState: values["SubState"],
		FragmentPath: values["FragmentPath"], SourcePath: values["SourcePath"], DropInPaths: values["DropInPaths"], UnitFileState: values["UnitFileState"],
		Transient: transient, NeedDaemonReload: reload, Job: uint32(job), NoNewPrivileges: noNewPrivileges}, nil
}

func absentProperties(p Properties) bool {
	return p.LoadState == "not-found" && p.ActiveState == "inactive" && p.SubState == "dead" && p.FragmentPath == "" && p.SourcePath == "" && p.DropInPaths == "" && !p.Transient && !p.NeedDaemonReload && p.Job == 0 && (p.UnitFileState == "" || p.UnitFileState == "not-found")
}

func inactive(p Properties) bool {
	return (p.ActiveState == "inactive" || p.ActiveState == "failed") && p.Job == 0
}
