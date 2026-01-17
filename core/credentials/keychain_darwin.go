//go:build darwin

package credentials

import (
	"os/exec"
	"strings"
)

type darwinKeychain struct{}

func newPlatformKeychain() KeychainProvider {
	return &darwinKeychain{}
}

func (k *darwinKeychain) Available() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

func (k *darwinKeychain) Get(service, account string) (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (k *darwinKeychain) Set(service, account, secret string) error {
	_ = k.Delete(service, account)

	cmd := exec.Command("security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", secret,
		"-U",
	)
	return cmd.Run()
}

func (k *darwinKeychain) Delete(service, account string) error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	)
	return cmd.Run()
}
