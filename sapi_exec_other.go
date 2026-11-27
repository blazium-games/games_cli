//go:build !linux

package main

import "os"

func sealSapiImage(body []byte, pin string) (*os.File, error) {
	return nil, usageErrorf("the cached emulator only runs on Linux amd64: pass --binary")
}

func sapiImagePath(f *os.File) string {
	return f.Name()
}
