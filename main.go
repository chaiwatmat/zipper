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
	"strings"
)

var (
	srcPath        string
	targetZip      string
	writeHash      bool
	gpgSign        bool
	copyTo         string
	netUser        string
	netPass        string
	useRobocopy    bool
	verifyOnTarget bool
	dryRun         bool
)

func init() {
	flag.StringVar(&srcPath, "src", "", "Source file or directory to zip")
	flag.StringVar(&targetZip, "out", "output.zip", "Output zip file name")
	flag.BoolVar(&writeHash, "hash", false, "Write SHA256 hash of zip file")
	flag.BoolVar(&gpgSign, "sign", false, "Sign the SHA256 file using GPG")
	flag.StringVar(&copyTo, "copyto", "", "UNC path to copy files to")
	flag.StringVar(&netUser, "user", "", "Username for network share")
	flag.StringVar(&netPass, "pass", "", "Password for network share")
	flag.BoolVar(&useRobocopy, "useRobocopy", false, "Use robocopy instead of regular copy")
	flag.BoolVar(&verifyOnTarget, "verifyTarget", false, "Verify SHA256 after copy")
	flag.BoolVar(&dryRun, "dryrun", false, "Simulate all actions without file creation or copy")
}

func main() {
	flag.Parse()
	if srcPath == "" {
		fmt.Println("❌ Please provide -src")
		os.Exit(1)
	}

	// Zip step
	if dryRun {
		fmt.Printf("[DRYRUN] Would zip %s → %s\n", srcPath, targetZip)
	} else {
		err := zipFolder(srcPath, targetZip)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Zip error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Zip completed")
	}

	// Hash step
	if writeHash {
		hashFile := targetZip + ".sha256"
		if dryRun {
			fmt.Printf("[DRYRUN] Would generate SHA256 → %s\n", hashFile)
		} else {
			err := writeHashFile(targetZip)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Hash error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ Hash file created")
		}
	}

	// Sign step
	if gpgSign && writeHash {
		sigFile := targetZip + ".sha256.asc"
		if dryRun {
			fmt.Printf("[DRYRUN] Would sign %s → %s\n", targetZip+".sha256", sigFile)
		} else {
			err := signWithGpg(targetZip + ".sha256")
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ GPG sign error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ Signature file created")
		}
	}

	// File list to copy
	filesToCopy := []string{targetZip}
	if writeHash {
		filesToCopy = append(filesToCopy, targetZip+".sha256")
	}
	if gpgSign && writeHash {
		filesToCopy = append(filesToCopy, targetZip+".sha256.asc")
	}

	// Copy step
	if copyTo != "" {
		var err error
		if useRobocopy {
			err = copyWithRobocopy(copyTo, filesToCopy, netUser, netPass, dryRun)
		} else {
			err = copyToWindowsShare(copyTo, filesToCopy, netUser, netPass, dryRun)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Copy error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Copy completed")
	}

	// Verify step
	if verifyOnTarget && writeHash {
		if dryRun {
			fmt.Printf("[DRYRUN] Would verify SHA256 on %s\n", copyTo)
		} else {
			err := verifyHashOnTarget(copyTo, targetZip)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Hash verification failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ Remote file hash verified successfully")
		}
	}
}

func zipFolder(src, out string) error {
	outFile, err := os.Create(out)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(filepath.Dir(src), path)
		fw, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}
		fr, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fr.Close()
		_, err = io.Copy(fw, fr)
		return err
	})
}

func writeHashFile(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	hashLine := fmt.Sprintf("%s  %s\n", hash, filepath.Base(filePath))
	return os.WriteFile(filePath+".sha256", []byte(hashLine), 0644)
}

func signWithGpg(file string) error {
	cmd := exec.Command("gpg", "--armor", "--pinentry-mode", "loopback", "--output", file+".asc", "--sign", file)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gpg error: %s\n%s", err, output)
	}
	return nil
}

func copyToWindowsShare(uncPath string, files []string, user, pass string, dryRun bool) error {
	if dryRun {
		fmt.Println("[DRYRUN] Would connect to:", uncPath)
		for _, file := range files {
			fmt.Printf("[DRYRUN] Would copy %s → %s\n", file, filepath.Join(uncPath, filepath.Base(file)))
		}
		return nil
	}

	args := []string{"net", "use", uncPath}
	if user != "" && pass != "" {
		args = append(args, pass, "/user:"+user)
	}
	args = append(args, "/persistent:no")
	cmd := exec.Command("cmd", "/C", strings.Join(args, " "))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("net use failed: %s\n%s", err, output)
	}

	for _, file := range files {
		dest := filepath.Join(uncPath, filepath.Base(file))
		src, err := os.Open(file)
		if err != nil {
			return err
		}
		defer src.Close()

		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer out.Close()

		if _, err = io.Copy(out, src); err != nil {
			return err
		}
	}

	_ = exec.Command("cmd", "/C", "net", "use", uncPath, "/delete", "/yes").Run()
	return nil
}

func copyWithRobocopy(uncPath string, files []string, user, pass string, dryRun bool) error {
	if dryRun {
		fmt.Println("[DRYRUN] Would robocopy to:", uncPath)
		for _, f := range files {
			fmt.Printf("[DRYRUN] Would robocopy file: %s\n", f)
		}
		return nil
	}

	args := []string{"net", "use", uncPath}
	if user != "" && pass != "" {
		args = append(args, pass, "/user:"+user)
	}
	args = append(args, "/persistent:no")
	cmd := exec.Command("cmd", "/C", strings.Join(args, " "))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("net use failed: %s\n%s", err, output)
	}

	group := map[string][]string{}
	for _, f := range files {
		dir := filepath.Dir(f)
		group[dir] = append(group[dir], filepath.Base(f))
	}

	for dir, names := range group {
		cmdArgs := append([]string{dir, uncPath}, names...)
		cmdArgs = append(cmdArgs, "/Z", "/R:3", "/W:5", "/NFL", "/NDL")
		roboCmd := exec.Command("robocopy", cmdArgs...)
		if output, err := roboCmd.CombinedOutput(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() >= 8 {
				return fmt.Errorf("robocopy failed: %s\n%s", err, output)
			}
		}
	}

	_ = exec.Command("cmd", "/C", "net", "use", uncPath, "/delete", "/yes").Run()
	return nil
}

func verifyHashOnTarget(uncPath, localZip string) error {
	zipName := filepath.Base(localZip)
	remoteZip := filepath.Join(uncPath, zipName)
	remoteSha := filepath.Join(uncPath, zipName+".sha256")

	hashData, err := os.ReadFile(remoteSha)
	if err != nil {
		return err
	}
	expected := strings.ToUpper(strings.Fields(string(hashData))[0])

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
	if actual != expected {
		return fmt.Errorf("hash mismatch:\nExpected: %s\nActual:   %s", expected, actual)
	}
	return nil
}
