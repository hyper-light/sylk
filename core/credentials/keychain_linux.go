//go:build linux

package credentials

import (
	"os/exec"
	"strings"
)

type linuxKeychain struct{}

func newPlatformKeychain() KeychainProvider {
	return &linuxKeychain{}
}

func (k *linuxKeychain) Available() bool {
	_, err := exec.LookPath("secret-tool")
	return err == nil
}

func (k *linuxKeychain) Get(service, account string) (string, error) {
	cmd := exec.Command("secret-tool", "lookup",
		"service", service,
		"account", account,
	)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (k *linuxKeychain) Set(service, account, secret string) error {
	cmd := exec.Command("secret-tool", "store",
		"--label", service+" - "+account,
		"service", service,
		"account", account,
	)
	cmd.Stdin = strings.NewReader(secret)
	return cmd.Run()
}

func (k *linuxKeychain) Delete(service, account string) error {
	cmd := exec.Command("secret-tool", "clear",
		"service", service,
		"account", account,
	)
	return cmd.Run()
}
