//go:build windows

package resources

func getFileRlimit() (uint64, error) {
	return 1024, nil
}
