package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

func readPinnedControlFile(path, label string, maxBytes int64) ([]byte, error) {
	initial, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !initial.Mode().IsRegular() || initial.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is not a plain regular file", label)
	}
	file, err := openPinnedControlFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	handleInfo, err := file.Stat()
	if err != nil || !handleInfo.Mode().IsRegular() || !os.SameFile(initial, handleInfo) {
		return nil, fmt.Errorf("%s identity changed while opening", label)
	}
	readOnce := func() ([]byte, error) {
		data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
		if readErr != nil {
			return nil, readErr
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("%s is too large", label)
		}
		return data, nil
	}
	first, err := readOnce()
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	second, err := readOnce()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(first, second) {
		return nil, fmt.Errorf("%s content changed while reading", label)
	}
	afterHandle, err := file.Stat()
	if err != nil || !os.SameFile(handleInfo, afterHandle) || handleInfo.Size() != afterHandle.Size() || !handleInfo.ModTime().Equal(afterHandle.ModTime()) {
		return nil, fmt.Errorf("%s changed while reading", label)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(handleInfo, current) {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%s identity changed while reading", label)
	}
	return first, nil
}
