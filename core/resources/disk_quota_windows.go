//go:build windows

package resources

func statFs(_ string) (uint64, uint64, error) {
	return 0, 0, nil
}
