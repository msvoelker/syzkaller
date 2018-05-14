// Copyright 2017 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

//go:generate bash -c "echo -en '// AUTOGENERATED FILE\n\n' > generated.go"
//go:generate bash -c "echo -en 'package kernel\n\n' >> generated.go"
//go:generate bash -c "echo -en 'const createImageScript = `#!/bin/bash\n' >> generated.go"
//go:generate bash -c "cat ../../tools/create-gce-image.sh | grep -v '#' >> generated.go"
//go:generate bash -c "echo -en '`\n\n' >> generated.go"

// Package kernel contains helper functions for working with Linux kernel
// (building kernel/image).
package kernel

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/syzkaller/pkg/osutil"
)

func Build(dir, compiler string, config []byte) error {
	configFile := filepath.Join(dir, ".config")
	if err := osutil.WriteFile(configFile, config); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}
	if err := osutil.SandboxChown(configFile); err != nil {
		return err
	}
	cmd := osutil.Command("make", "olddefconfig")
	if err := osutil.Sandbox(cmd, true, true); err != nil {
		return err
	}
	cmd.Dir = dir
	if _, err := osutil.Run(10*time.Minute, cmd); err != nil {
		return err
	}
	// We build only bzImage as we currently don't use modules.
	cpu := strconv.Itoa(runtime.NumCPU())
	cmd = osutil.Command("make", "bzImage", "-j", cpu, "CC="+compiler)
	if err := osutil.Sandbox(cmd, true, true); err != nil {
		return err
	}
	cmd.Dir = dir
	// Build of a large kernel can take a while on a 1 CPU VM.
	_, err := osutil.Run(3*time.Hour, cmd)
	return err
}

func Clean(dir string) error {
	cpu := strconv.Itoa(runtime.NumCPU())
	cmd := osutil.Command("make", "distclean", "-j", cpu)
	if err := osutil.Sandbox(cmd, true, true); err != nil {
		return err
	}
	cmd.Dir = dir
	_, err := osutil.Run(10*time.Minute, cmd)
	return err
}

// CreateImage creates a disk image that is suitable for syzkaller.
// Kernel is taken from kernelDir, userspace system is taken from userspaceDir.
// If cmdlineFile is not empty, contents of the file are appended to the kernel command line.
// If sysctlFile is not empty, contents of the file are appended to the image /etc/sysctl.conf.
// Produces image and root ssh key in the specified files.
func CreateImage(kernelDir, userspaceDir, cmdlineFile, sysctlFile, image, sshkey string) error {
	tempDir, err := ioutil.TempDir("", "syz-build")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	scriptFile := filepath.Join(tempDir, "create.sh")
	if err := osutil.WriteExecFile(scriptFile, []byte(createImageScript)); err != nil {
		return fmt.Errorf("failed to write script file: %v", err)
	}
	bzImage := filepath.Join(kernelDir, filepath.FromSlash("arch/x86/boot/bzImage"))
	cmd := osutil.Command(scriptFile, userspaceDir, bzImage)
	cmd.Dir = tempDir
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env,
		"SYZ_CMDLINE_FILE="+osutil.Abs(cmdlineFile),
		"SYZ_SYSCTL_FILE="+osutil.Abs(sysctlFile),
	)
	if _, err = osutil.Run(time.Hour, cmd); err != nil {
		return fmt.Errorf("image build failed: %v", err)
	}
	// Note: we use CopyFile instead of Rename because src and dst can be on different filesystems.
	if err := osutil.CopyFile(filepath.Join(tempDir, "disk.raw"), image); err != nil {
		return err
	}
	if err := osutil.CopyFile(filepath.Join(tempDir, "key"), sshkey); err != nil {
		return err
	}
	if err := os.Chmod(sshkey, 0600); err != nil {
		return err
	}
	return nil
}

func CompilerIdentity(compiler string) (string, error) {
	output, err := osutil.RunCmd(time.Minute, "", compiler, "--version")
	if err != nil {
		return "", err
	}
	if len(output) == 0 {
		return "", fmt.Errorf("no output from compiler --version")
	}
	return strings.Split(string(output), "\n")[0], nil
}
