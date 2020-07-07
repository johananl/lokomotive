package util

import (
	"archive/zip"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunAdmissionWebhook is used to create all required resources to start mutating admission controller.
func RunAdmissionWebhook(kubeconfigAbsoutePath string) error {
	dir, err := ioutil.TempDir("", "mutating")
	if err != nil {
		return fmt.Errorf("error in creating directory: %v", err)
	}

	defer func() {
		err = os.RemoveAll(dir)
		if err != nil {
			fmt.Printf("error when removing files: %v", err)
		}
	}()

	//nolint:lll
	fileURL := "https://gist.github.com/knrt10/274228a6626894f97466751a30f82b87/archive/ebddf3300eb8f4b8d668dc90745da0a4bc4b9c29.zip"

	err = downloadFile(dir+"/install.zip", fileURL)
	if err != nil {
		return fmt.Errorf("error in dowloading zip file: %v", err)
	}

	_, err = unzip(dir+"/install.zip", dir)
	if err != nil {
		return fmt.Errorf("error in unzipping file: %v", err)
	}

	_, err = exec.Command("chmod", "777", dir+"/webhook-run.sh").Output() //nolint:gosec
	if err != nil {
		return fmt.Errorf("changing file permission failed failed: %v", err)
	}

	_, err = exec.Command("/bin/sh", dir+"/webhook-run.sh", "--kubeconfigpath", kubeconfigAbsoutePath).Output() //nolint:gosec
	if err != nil {
		return fmt.Errorf("applying mutating admission webhook failed: %v", err)
	}

	return nil
}

// downloadFile will download a url to a local file. It's efficient because it will
// write as it downloads and not load the whole file into memory.
func downloadFile(filepath string, url string) error {
	// Get the data
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}

	defer func() {
		err = resp.Body.Close()
		if err != nil {
			fmt.Printf("error when closing response body: %v", err)
		}
	}()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}

	defer func() {
		err = out.Close()
		if err != nil {
			fmt.Printf("error when closing file: %v", err)
		}
	}()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

//nolint
// unzip is used to unzip the desired zip file.
func unzip(src string, dest string) ([]string, error) {
	var filenames []string //nolint:gosec

	r, err := zip.OpenReader(src)
	if err != nil {
		return filenames, err
	}

	defer func() {
		err = r.Close()
		if err != nil {
			fmt.Printf("error when closing file: %v", err)
		}
	}()

	for _, f := range r.File {
		fileSlicePath := strings.Split(f.Name, "/")
		// Store filename/path for returning and using later on
		fpath := filepath.Join(dest, fileSlicePath[1], "/")

		filenames = append(filenames, fpath)
		if f.FileInfo().IsDir() {
			// Make Folder
			err = os.MkdirAll(fpath, os.ModePerm)
			if err != nil {
				return filenames, err
			}

			continue
		}

		// Make File
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return filenames, err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return filenames, err
		}

		rc, err := f.Open()
		if err != nil {
			return filenames, err
		}

		_, err = io.Copy(outFile, rc)
		// Close the file without defer to close before next iteration of loop
		err = outFile.Close()
		if err != nil {
			fmt.Printf("error in closing file %v:", err)
		}

		err = rc.Close()
		if err != nil {
			fmt.Printf("error in closing file %v:", err)
		}

		if err != nil {
			return filenames, err
		}
	}

	return filenames, nil
}
