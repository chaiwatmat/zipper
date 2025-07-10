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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/schollz/progressbar/v3"
)

var (
	sourcePath   string
	targetZip    string
	compression  string
	writeHash    bool
	excludeGlobs arrayFlags
	workers      int
)

type arrayFlags []string

func (i *arrayFlags) String() string { return "Exclude patterns" }
func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type fileJob struct {
	relPath  string
	fullPath string
	info     os.FileInfo
}

func init() {
	flag.StringVar(&sourcePath, "src", "", "Source file or directory to zip")
	flag.StringVar(&targetZip, "out", "output.zip", "Output zip file path")
	flag.StringVar(&compression, "level", "default", "Compression level: store, fastest, default, best")
	flag.BoolVar(&writeHash, "hash", false, "Write SHA256 hash file (output.zip.sha256)")
	flag.Var(&excludeGlobs, "exclude", "Exclude pattern (repeatable, e.g., -exclude **/*.log -exclude .git/**)")
	flag.IntVar(&workers, "threads", runtime.NumCPU(), "Number of parallel workers for directories")
	flag.Parse()
}

func main() {
	if sourcePath == "" {
		fmt.Fprintln(os.Stderr, "❌ Please specify source path with -src")
		os.Exit(1)
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Invalid source path: %v\n", err)
		os.Exit(1)
	}

	err = zipFileOrDir(sourcePath, targetZip, info)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to zip: %v\n", err)
		os.Exit(1)
	}

	if writeHash {
		err = writeSHA256(targetZip, targetZip+".sha256")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to write hash: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ SHA256 written to %s\n", targetZip+".sha256")
	}

	fmt.Printf("✅ Zip completed: %s\n", targetZip)
}

func zipFileOrDir(source, output string, info os.FileInfo) error {
	outFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	level := zip.Deflate
	if strings.ToLower(compression) == "store" {
		level = zip.Store
	}

	if !info.IsDir() {
		// Single file
		return addFileToZip(zipWriter, source, filepath.Base(source), info, level)
	}

	// Directory — multithreaded
	fileList := []fileJob{}
	err = filepath.Walk(source, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(source, path)
		rel = filepath.ToSlash(rel)

		for _, pattern := range excludeGlobs {
			match, _ := doublestar.PathMatch(pattern, rel)
			if match {
				return nil
			}
		}
		fileList = append(fileList, fileJob{relPath: rel, fullPath: path, info: fi})
		return nil
	})
	if err != nil {
		return err
	}

	var mutex sync.Mutex
	bar := progressbar.Default(int64(len(fileList)))
	jobs := make(chan fileJob)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				err := func() error {
					header, _ := zip.FileInfoHeader(job.info)
					header.Name = job.relPath
					header.Method = level
					header.Modified = time.Time{} // deterministic

					mutex.Lock()
					writer, err := zipWriter.CreateHeader(header)
					mutex.Unlock()
					if err != nil {
						return err
					}

					f, err := os.Open(job.fullPath)
					if err != nil {
						return err
					}
					defer f.Close()

					_, err = io.Copy(writer, f)
					return err
				}()
				if err != nil {
					fmt.Fprintf(os.Stderr, "\n⚠️ Failed to zip %s: %v\n", job.relPath, err)
				}
				bar.Add(1)
			}
		}()
	}

	for _, job := range fileList {
		jobs <- job
	}
	close(jobs)
	wg.Wait()
	return nil
}

func addFileToZip(zipWriter *zip.Writer, path, rel string, info os.FileInfo, level uint16) error {
	header, _ := zip.FileInfoHeader(info)
	header.Name = filepath.ToSlash(rel)
	header.Method = level
	header.Modified = time.Time{} // make deterministic

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(writer, f)
	return err
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
