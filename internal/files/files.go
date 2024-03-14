package files

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/borderzero/border0-cli/internal/util"
)

// DotBorder0Dir returns the path to the .border0 directory in the user's home directory.
// If the directory does not exist, it will be created.
func DotBorder0Dir() (string, error) {
	home, err := util.GetUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}

	dotBorder0Dir := filepath.Join(home, ".border0")
	if !Exists(dotBorder0Dir) {
		// use 0700 to make sure the directory is only readable and writable by the user
		if err := os.Mkdir(dotBorder0Dir, 0700); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", dotBorder0Dir, err)
		}
	}

	return dotBorder0Dir, nil
}

// Exists returns true if the file or path exists.
func Exists(fileOrPath string) bool {
	_, err := os.Stat(fileOrPath)
	return os.IsNotExist(err) == false
}

// ReadIntoString reads the file at the given path and returns its content as a string.
func ReadIntoString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return string(data), nil
}

// WriteStringToFile writes the given string data to the file at the given path.
func WriteStringToFile(path, data string) error {
	// use 0600 to make sure the file is only readable by the user
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}
