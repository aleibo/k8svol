package flex

import (
	"encoding/json"
	"fmt"
	"github.com/v3io/k8svol/pkg/journal"
	"os"
	"os/exec"
	"path"
	"time"
)

type Mounter struct {
	Target string
	Spec   *VolumeSpec

	Config *Config
}

func (m *Mounter) doMount(targetPath string) *Response {
	session, err := m.Config.DataSession(m.Spec)
	if err != nil {
		return Fail("Could not create session", err)
	}

	dataUrls, err := m.Config.DataURLs(m.Spec.GetClusterName())
	if err != nil {
		return Fail("could not get cluster data urls", err)
	}

	args := []string{"-o", "allow_other",
		"--connection_strings", dataUrls,
		"--mountpoint", targetPath,
		"--session_key", session}
	if m.Spec.Container != "" {
		args = append(args, "-a", m.Spec.Container)
	}
	mountCmd := exec.Command(m.Config.FusePath, args...)

	journal.Debug("calling mount command", "path", mountCmd.Path, "args", mountCmd.Args)
	if err := mountCmd.Start(); err != nil {
		return Fail(fmt.Sprintf("Could not mount: %s", m.Target), err)
	}
	for _, interval := range []time.Duration{1, 2, 4} {
		if isMountPoint(targetPath) {
			return Success("Mount completed!")
		}
		time.Sleep(interval * time.Second)
	}
	return Fail(fmt.Sprintf("Could not mount due to timeout: %s", m.Target), nil)
}

func (m *Mounter) osMount() *Response {
	journal.Info("calling osMount command", "target", m.Target)
	if isStaleMount(m.Target) {
		unmountCmd := exec.Command("umount", m.Target)
		out, err := unmountCmd.CombinedOutput()
		if err != nil {
			return Fail(fmt.Sprintf("Could not unmount stale mount %s: %s", m.Target, out), err)
		}
	}

	if !isMountPoint(m.Target) {
		return m.doMount(m.Target)
	}
	return Success(fmt.Sprintf("Already mounted: %s", m.Target))
}

func (m *Mounter) Mount() *Response {
	if m.Config.Type == "link" {
		return m.mountAsLink()
	}
	return m.osMount()
}

func (m *Mounter) mountAsLink() *Response {
	journal.Info("calling mountAsLink command", "target", m.Target)
	targetPath := path.Join("/mnt/v3io", m.Spec.Namespace, m.Spec.Container)
	response := &Response{}
	if !isMountPoint(targetPath) {
		journal.Debug("creating folder", "target", targetPath)
		os.MkdirAll(targetPath, 0755)
		response = m.doMount(targetPath)
	}

	if err := os.Remove(m.Target); err != nil {
		m.Unmount()
		return Fail(fmt.Sprintf("unable to remove target %s", m.Target), err)
	}

	os.Symlink(targetPath, m.Target)
	return response
}

func (m *Mounter) unmountAsLink() *Response {
	journal.Info("calling unmountAsLink command", "target", m.Target)
	if err := os.Remove(m.Target); err != nil {
		return Fail("unable to remove link", err)
	}
	return Success("link removed")
}

func (m *Mounter) osUmount() *Response {
	journal.Info("calling osUmount command", "target", m.Target)
	if isMountPoint(m.Target) {
		cmd := exec.Command("umount", m.Target)
		journal.Debug("calling umount command", "path", cmd.Path, "args", cmd.Args)
		if err := cmd.Start(); err != nil {
			return Fail("could not unmount", err)
		}
		for _, interval := range []time.Duration{1, 2, 4} {
			if !isMountPoint(m.Target) {
				return Success("Unmount completed!")
			}
			time.Sleep(interval * time.Second)
		}
		return Fail(fmt.Sprintf("Could not umount due to timeout: %s", m.Target), nil)
	}
	return Success("Unmount completed!")
}

func (m *Mounter) Unmount() *Response {
	if m.Config.Type == "link" {
		return m.unmountAsLink()
	}
	return m.osUmount()
}

func NewMounter(target, options string) (*Mounter, error) {
	opts := VolumeSpec{}
	if options != "" {
		if err := json.Unmarshal([]byte(options), &opts); err != nil {
			return nil, err
		}
	}
	journal.Debug("reading config")
	config, err := ReadConfig()
	if err != nil {
		return nil, err
	}
	return &Mounter{
		Target: target,
		Config: config,
		Spec:   &opts,
	}, nil
}

func Mount(target, options string) *Response {
	mounter, err := NewMounter(target, options)
	if err != nil {
		return Fail("unable to create mounter", err)
	} else {
		return mounter.Mount()
	}
}

func Unmount(target string) *Response {
	mounter, err := NewMounter(target, "")
	if err != nil {
		return Fail("unable to create mounter", err)
	} else {
		return mounter.Unmount()
	}
}

func Init() *Response {
	journal.Info("calling init command")
	config, err := ReadConfig()
	if err != nil {
		return Fail("Initialization script failed to read config", err)
	}
	_, staterr := os.Stat(config.FusePath)
	if staterr != nil {
		if os.IsNotExist(staterr) {
			location := path.Dir(os.Args[0])
			command := exec.Command("/bin/bash", path.Join(location, "install.sh"))
			journal.Debug("calling install command", "path", command.Path, "args", command.Args)
			if err := command.Run(); err != nil {
				return Fail("Initialization script failed", err)
			}
		} else {
			return Fail("Initialization script failed to get fuse status", staterr)
		}
	}

	resp := Success("Initialization completed")
	resp.Capabilities = map[string]interface{}{
		"attach": false,
	}
	return resp
}
