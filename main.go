package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
)

func main() {
	source := os.Args[1] // e.g., "build/output"
	target := os.Args[2] // e.g., "output.zip"

	zipfile, _ := os.Create(target)
	defer zipfile.Close()

	zipWriter := zip.NewWriter(zipfile)
	defer zipWriter.Close()

	filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(source, path)
		writer, _ := zipWriter.Create(relPath)
		file, _ := os.Open(path)
		defer file.Close()
		io.Copy(writer, file)
		return nil
	})
}

