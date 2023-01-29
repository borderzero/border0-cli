package preference

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_filePath(t *testing.T) {
	realOSUserConfigDir := osUserConfigDir
	realOSUserHomeDir := osUserHomeDir
	realOSMkdir := osMkdir
	t.Cleanup(func() {
		osUserConfigDir = realOSUserConfigDir
		osUserHomeDir = realOSUserHomeDir
		osMkdir = realOSMkdir
	})

	errUnitTest := errors.New("expected unit test error")

	existingUserConfigDir := t.TempDir()
	err := os.Mkdir(filepath.Join(existingUserConfigDir, "border0"), 0700)
	require.NoError(t, err)

	nonExistingUserConfigDir := filepath.Join(existingUserConfigDir, "not-exist")

	homeHasExistingDotDir := t.TempDir()
	err = os.Mkdir(filepath.Join(homeHasExistingDotDir, ".border0"), 0700)
	require.NoError(t, err)

	homeDoesNotHaveDotDir := t.TempDir()

	tests := []struct {
		name              string
		mockUserConfigDir func() (string, error)
		mockUserHomeDir   func() (string, error)
		mockMkdir         func(string, os.FileMode) error
		wantPath          string
		wantErr           error
	}{
		{
			name: "failed to get user config dir",
			mockUserConfigDir: func() (string, error) {
				return "", errUnitTest
			},
			wantPath: "",
			wantErr:  fmt.Errorf("failed to get user config dir: %w", errUnitTest),
		},
		{
			name: "border0 folder exists in os user config dir",
			mockUserConfigDir: func() (string, error) {
				return existingUserConfigDir, nil
			},
			wantPath: filepath.Join(existingUserConfigDir, "border0", "preference.json"),
			wantErr:  nil,
		},
		{
			name: "failed to get user home dir",
			mockUserConfigDir: func() (string, error) {
				return nonExistingUserConfigDir, nil
			},
			mockUserHomeDir: func() (string, error) {
				return "", errUnitTest
			},
			wantPath: "",
			wantErr:  fmt.Errorf("failed to get home dir: %w", errUnitTest),
		},
		{
			name: "failed to create .border0 folder in home dir",
			mockUserConfigDir: func() (string, error) {
				return nonExistingUserConfigDir, nil
			},
			mockUserHomeDir: func() (string, error) {
				return homeDoesNotHaveDotDir, nil
			},
			mockMkdir: func(path string, mode os.FileMode) error {
				return errUnitTest
			},
			wantPath: "",
			wantErr: fmt.Errorf("failed to create directory %s: %w",
				filepath.Join(homeDoesNotHaveDotDir, ".border0"), errUnitTest),
		},
		{
			name: "successfully created .border0 folder in home dir",
			mockUserConfigDir: func() (string, error) {
				return nonExistingUserConfigDir, nil
			},
			mockUserHomeDir: func() (string, error) {
				return homeDoesNotHaveDotDir, nil
			},
			mockMkdir: func(path string, mode os.FileMode) error {
				return os.Mkdir(path, mode)
			},
			wantPath: filepath.Join(homeDoesNotHaveDotDir, ".border0", "preference.json"),
			wantErr:  nil,
		},
		{
			name: ".border0 folder already exists in home dir",
			mockUserConfigDir: func() (string, error) {
				return nonExistingUserConfigDir, nil
			},
			mockUserHomeDir: func() (string, error) {
				return homeHasExistingDotDir, nil
			},
			wantPath: filepath.Join(homeHasExistingDotDir, ".border0", "preference.json"),
			wantErr:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			osUserConfigDir = test.mockUserConfigDir
			osUserHomeDir = test.mockUserHomeDir
			osMkdir = test.mockMkdir

			gotPath, err := filePath()
			assert.Equal(t, test.wantPath, gotPath)
			assert.Equal(t, test.wantErr, err)
		})
	}
}

func Test_fileOrPathExists(t *testing.T) {
	tests := []struct {
		name  string
		given string
		want  bool
	}{
		{
			name:  "happy path",
			given: t.TempDir(),
			want:  true,
		},
		{
			name:  "not exist",
			given: "file-not-exist",
			want:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, fileOrPathExists(test.given))
		})
	}
}
