//go:build windows

package credentials

type windowsKeychain struct{}

func newPlatformKeychain() KeychainProvider {
	return &windowsKeychain{}
}

func (k *windowsKeychain) Available() bool {
	return false
}

func (k *windowsKeychain) Get(service, account string) (string, error) {
	return "", nil
}

func (k *windowsKeychain) Set(service, account, secret string) error {
	return nil
}

func (k *windowsKeychain) Delete(service, account string) error {
	return nil
}
