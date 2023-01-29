package preference

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func Read() (*Data, error) {
	data := NewData()

	pathToFile, err := filePath()
	if err != nil {
		return data, fmt.Errorf("failed to get preference file path: %w", err)
	}

	if _, err := os.Stat(pathToFile); os.IsNotExist(err) {
		// file does not exist, but we don't want to error.
		// we'll just return an empty preference
		// First time this get's called, the file will be created on write.
		return data, nil
	}

	jsonFile, err := os.Open(pathToFile)
	if err != nil {
		return data, fmt.Errorf("failed to open %s: %w", pathToFile, err)
	}
	if err := json.NewDecoder(jsonFile).Decode(&data); err != nil {
		return data, fmt.Errorf("failed to decode %s: %w", pathToFile, err)
	}

	return data, nil
}

func Write(data *Data) error {
	pathToFile, err := filePath()
	if err != nil {
		return fmt.Errorf("failed to get preference file path: %w", err)
	}
	jsonFile, err := os.Create(pathToFile)
	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to create %s: %w", pathToFile, err)
	}
	encoder := json.NewEncoder(jsonFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode %s: %w", pathToFile, err)
	}

	return nil
}

var (
	osUserConfigDir = os.UserConfigDir
	osUserHomeDir   = os.UserHomeDir
	osMkdir         = os.Mkdir
)

func filePath() (string, error) {
	// if $HOME/.config/border0 already exists, for backward compatibility, return $HOME/.config/border0/preference.json
	// otherwise, return $HOME/.border0/preference.json, so it's consistent with other border0 token and cert files

	userConfigDir, err := osUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}

	border0ConfigDir := filepath.Join(userConfigDir, "border0")
	if fileOrPathExists(border0ConfigDir) {
		return filepath.Join(border0ConfigDir, "preference.json"), nil
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}

	dotBorder0Dir := filepath.Join(home, ".border0")
	if !fileOrPathExists(dotBorder0Dir) {
		if err := osMkdir(dotBorder0Dir, 0700); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", dotBorder0Dir, err)
		}
	}

	return filepath.Join(dotBorder0Dir, "preference.json"), nil
}

func fileOrPathExists(fileOrPath string) bool {
	_, err := os.Stat(fileOrPath)
	return os.IsNotExist(err) == false
}

func CreateOrUpdate(orgID, orgSubdomain string) error {
	if orgID == "" {
		return errors.New("WARNING: org ID is empty")
	}

	pref, err := Read()
	if err != nil {
		return fmt.Errorf("WARNING: could not read preference file: %w", err)
	}

	orgPref := pref.Org(orgID)
	orgPref.Subdomain = orgSubdomain
	pref.SetOrg(orgPref)

	if err := Write(pref); err != nil {
		return fmt.Errorf("WARNING: could not update preference file: %w", err)
	}

	return nil
}
