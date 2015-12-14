// Package tempedit contains a utility function for editing a file
// interactively.
package tempedit

import (
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
)

// Edit runs $EDITOR with a temp file that contains contents. It
// returns the final contents of the file after editing.
func Edit(contents []byte, path string) ([]byte, error) {
	var f *os.File
	var err error
	if path == "" {
		f, err = ioutil.TempFile("", "src")
	} else {
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if path == "" {
		defer os.Remove(f.Name())
	}

	if _, err := f.Write(contents); err != nil {
		return nil, err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		return nil, errors.New("no EDITOR environment variable set")
	}

	cmd := exec.Command(editor, f.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	// Seeking to the beginning and reading the file's contents does
	// not work reliably, for some reason. For example, if EDITOR=vi,
	// then it sees an empty file. So, just call ReadFile.
	return ioutil.ReadFile(f.Name())
}
