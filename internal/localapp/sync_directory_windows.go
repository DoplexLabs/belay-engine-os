//go:build windows

package localapp

// Windows does not support fsync on directory handles. The preceding file
// sync and atomic rename provide the durability boundary available here.
func syncDirectory(string) error {
	return nil
}
