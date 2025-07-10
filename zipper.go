package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/schollz/progressbar/v3"
)

var (
	sourcePath     string
	targetZip      string
	compression    string
	writeHash      bool
	excludeGlobs   arrayFlags
	workers        int
	copyTo         string
	netUser        string
	netPass        string
	useRobocopy    bool
	verifyOnTarget bool
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
	flag.StringVar(&copyTo, "copyto", "", "Copy zip file to UNC share path (e.g. \\\\host\\share)")
	flag.StringVar(&netUser, "user", "", "Username for net use (optional)")
	flag.StringVar(&netPass, "pass", "", "Password for net use (optional)")
	flag.BoolVar(&useRobocopy, "useRobocopy", false, "Use robocopy for network copy")
	flag.BoolVar(&verifyOnTarget, "verifyTarget", false, "Verify zip file hash after copying to share")
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

	if copyTo != "" {
		if useRobocopy {
			err := copyWithRobocopy(copyTo, targetZip, netUser, netPass)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Robocopy failed: %v\n", err)
				os.Exit(1)
			}
		} else {
			err := copyToWindowsShare(copyTo, targetZip, netUser, netPass)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Copy failed: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("✅ Copied zip to %s\n", copyTo)
	}

	if verifyOnTarget && writeHash {
		err := verifyHashOnTarget(copyTo, targetZip)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Remote hash check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Remote file hash verified successfully")
	}
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

func copyWithRobocopy(uncPath, zipFile, user, pass string) error {
	// Step 1: net use (if needed)
	mapCmd := []string{"net", "use", uncPath}
	if user != "" && pass != "" {
		mapCmd = append(mapCmd, pass, "/user:"+user)
	}
	mapCmd = append(mapCmd, "/persistent:no")

	cmd := exec.Command("cmd", "/C", strings.Join(mapCmd, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("net use failed: %v\n%s", err, output)
	}

	// Step 2: robocopy
	srcDir := filepath.Dir(zipFile)
	fileName := filepath.Base(zipFile)
	roboCmd := exec.Command("robocopy", srcDir, uncPath, fileName, "/Z", "/R:3", "/W:5", "/NFL", "/NDL")
	roboOut, err := roboCmd.CombinedOutput()
	if err != nil {
		// robocopy returns non-zero even on success — must check exit code
		exitErr, ok := err.(*exec.ExitError)
		if ok && exitErr.ExitCode() >= 8 {
			return fmt.Errorf("robocopy failed: %v\n%s", err, roboOut)
		}
	}
	fmt.Print(string(roboOut))

	// Step 3: net use /delete
	_ = exec.Command("cmd", "/C", "net", "use", uncPath, "/delete", "/yes").Run()
	return nil
}

func copyToWindowsShare(uncPath, zipFile, user, pass string) error {
	// Step 1: Map network share
	mapCmd := []string{"net", "use", uncPath}
	if user != "" && pass != "" {
		mapCmd = append(mapCmd, pass, "/user:"+user)
	}
	mapCmd = append(mapCmd, "/persistent:no")

	cmd := exec.Command("cmd", "/C", strings.Join(mapCmd, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("net use failed: %s\n%s", err, output)
	}

	// Step 2: Copy file to share
	dest := filepath.Join(uncPath, filepath.Base(zipFile))
	srcData, err := os.Open(zipFile)
	if err != nil {
		return err
	}
	defer srcData.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create file on share: %v", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcData)
	if err != nil {
		return err
	}

	// Step 3: Disconnect
	disconnect := exec.Command("cmd", "/C", "net", "use", uncPath, "/delete", "/yes")
	disconnect.Run() // don't fail on disconnect error

	return nil
}

func verifyHashOnTarget(uncPath, localZip string) error {
	zipName := filepath.Base(localZip)
	hashFile := zipName + ".sha256"

	remoteZip := filepath.Join(uncPath, zipName)
	remoteHash := filepath.Join(uncPath, hashFile)

	// Read expected hash from .sha256 file
	hashBytes, err := os.ReadFile(remoteHash)
	if err != nil {
		return fmt.Errorf("failed to read remote .sha256: %w", err)
	}
	expected := strings.TrimSpace(string(hashBytes))

	// Calculate remote file hash using certutil (Windows-native)
	cmd := exec.Command("certutil", "-hashfile", remoteZip, "SHA256")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil failed: %s\n%s", err, out)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("unexpected certutil output:\n%s", out)
	}

	actual := strings.TrimSpace(lines[1])
	expected = strings.ToUpper(expected) // certutil uses uppercase

	if actual != expected {
		return fmt.Errorf("hash mismatch:\nExpected: %s\nActual:   %s", expected, actual)
	}

	return nil
}
