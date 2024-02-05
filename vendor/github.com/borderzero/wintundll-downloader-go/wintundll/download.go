package wintundll

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func downloadAndMoveFromZip(httpClient http.Client, url string, relativePathInUnzipped string, targetAbsolutePath string) error {
	tempdir, err := os.MkdirTemp("", "wintundll-download-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory for storing wintun: %v", err)
	}
	defer os.RemoveAll(tempdir)

	unzipDir, err := downloadAndUnzip(httpClient, url, tempdir)
	if err != nil {
		return fmt.Errorf("failed to download and unzip file: %v", err)
	}

	// rename (move) the file.
	err = os.Rename(filepath.Join(unzipDir, relativePathInUnzipped), targetAbsolutePath)
	if err != nil {
		return fmt.Errorf("failed to move file from ${UNZIPPED_ROOT}/%s to %s: %v", relativePathInUnzipped, targetAbsolutePath, err)
	}

	return nil
}

func downloadAndUnzip(httpClient http.Client, url string, dir string) (string, error) {
	downloadDir := filepath.Join(dir, "download")
	if err := os.Mkdir(downloadDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create subdirectory to store download contents: %v", err)
	}
	unzipDir := filepath.Join(dir, "unzip")
	if err := os.Mkdir(unzipDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create subdirectory to store unzipped contents: %v", err)
	}

	zipFile, err := os.CreateTemp(downloadDir, "*.zip")
	if err != nil {
		return "", fmt.Errorf("failed to create a temporary file for download: %v", err)
	}
	defer zipFile.Close()

	if err := download(httpClient, url, zipFile); err != nil {
		return "", fmt.Errorf("failed to download file at %s: %v", url, err)
	}

	if err := unzip(zipFile.Name(), unzipDir); err != nil {
		return "", fmt.Errorf("failed to unzip the downloaded file: %v", err)
	}

	return unzipDir, nil
}

func download(httpClient http.Client, url string, target io.Writer) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to make http request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got a non-200 status code (%d)", resp.StatusCode)
	}

	_, err = io.Copy(target, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write downloaded file contents to target location: %v", err)
	}

	return nil
}

func unzip(zipFilePath, contentsTarget string) error {
	r, err := zip.OpenReader(zipFilePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(contentsTarget, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		infile, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, infile)
		outFile.Close()
		infile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
