package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	sourceDir   string
	targetZip   string
	compression string
	writeHash   bool
)

func init() {
	flag.StringVar(&sourceDir, "src", "", "Source directory to zip")
	flag.StringVar(&targetZip, "out", "output.zip", "Output zip file path")
	flag.StringVar(&compression, "level", "default", "Compression level: store, fastest, default, best")
	flag.BoolVar(&writeHash, "hash", false, "Write SHA256 hash file (output.zip.sha256)")
	flag.Parse()
}

func main() {
	if sourceDir == "" {
		fmt.Fprintln(os.Stderr, "❌ Please specify source directory with -src")
		os.Exit(1)
	}

	zipLevel := zip.Deflate
	switch strings.ToLower(compression) {
	case "store":
		zipLevel = zip.Store
	case "fastest":
		zipLevel = zip.Deflate // Simpler level control can be tuned if needed
	case "default":
		zipLevel = zip.Deflate
	case "best":
		zipLevel = zip.Deflate
	default:
		fmt.Fprintln(os.Stderr, "❌ Unknown compression level")
		os.Exit(1)
	}

	zipFile, err := os.Create(targetZip)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create zip: %v\n", err)
		os.Exit(1)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Normalize for Windows
		relPath = filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zipLevel

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error zipping files: %v\n", err)
		os.Exit(1)
	}

	if writeHash {
		hashFile := targetZip + ".sha256"
		err = writeSHA256(targetZip, hashFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to write hash: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ SHA256 written to %s\n", hashFile)
	}

	fmt.Printf("✅ Zip created: %s\n", targetZip)
}

func writeSHA256(zipPath, hashPath string) error {
	f, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}

	hashSum := hex.EncodeToString(hasher.Sum(nil))
	return os.WriteFile(hashPath, []byte(hashSum), 0644)
}
