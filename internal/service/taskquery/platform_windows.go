package taskquery

import (
	"time"

	"github.com/b1ug/nb1/internal/service/taskuser"
)

func currentIdentity() (identity, error) {
	owner, err := taskuser.Current()
	if err != nil || !owner.Valid() {
		return identity{}, ErrIdentity
	}
	return identity{valid: true, sid: owner.SID(), taskPath: owner.TaskPath(), session: owner.SessionID()}, nil
}

func newPlatformReader() (*Reader, error) {
	owner, err := currentIdentity()
	if err != nil {
		return nil, err
	}
	directory, err := systemDirectory()
	if err != nil {
		return nil, ErrTrust
	}
	binary, windows, paths, ok := systemPaths(directory)
	if !ok {
		return nil, ErrTrust
	}
	check := func() error { return checkSystemFiles(paths) }
	if check() != nil {
		return nil, ErrTrust
	}
	bridge := nativeRunner{binary: binary, directory: directory, checkTrust: check,
		environment: []string{"SystemRoot=" + windows, "WINDIR=" + windows, "PATH=" + directory, "PSModulePath="}}
	return &Reader{owner: owner, current: currentIdentity, bridge: bridge, now: time.Now}, nil
}
