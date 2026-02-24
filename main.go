package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
)

const MinioPort = "9000"

type ProgressWriter struct {
	Total      int64
	Downloaded int64
	FileName   string
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.Downloaded += int64(n)

	if pw.Total > 0 {
		percentage := float64(pw.Downloaded) / float64(pw.Total) * 100
		fmt.Printf(
			"\r📥 Downloading %s: %.2f%% (%.2f/%.2f MB)",
			pw.FileName,
			percentage,
			float64(pw.Downloaded)/1024/1024,
			float64(pw.Total)/1024/1024,
		)
	} else {
		fmt.Printf(
			"\r📥 Downloading %s: %.2f MB",
			pw.FileName,
			float64(pw.Downloaded)/1024/1024,
		)
	}

	return n, nil
}

func main() {
	fmt.Printf("🛡️  BAREVAULT: Starting on %s (%s)...\n", runtime.GOOS, runtime.GOARCH)

	fmt.Println("🔍 Step 1: Checking infrastructure dependencies...")
	binDir, err := prepareEnvironment()
	if err != nil {
		log.Fatalf("❌ ERROR during setup: %v", err)
	}
	fmt.Println("✅ Step 1 Complete: Infrastructure binaries are ready.")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	fmt.Println("Step 2: Igniting Storage and Tunnel engines...")

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Engine: Initializing MinIO storage...")
		runMinio(ctx, binDir)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Engine: Establishing Cloudflare Tunnel...")
		runTunnel(ctx, binDir)
	}()

	wg.Wait()
}

func prepareEnvironment() (string, error) {
	home, _ := os.UserHomeDir()
	binDir := filepath.Join(home, ".barevault", "bin")

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create bin directory: %v", err)
	}

	required := []string{"cloudflared", "minio"}

	for _, name := range required {
		binaryName := name
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}

		path := filepath.Join(binDir, binaryName)

		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Printf("⚠️  %s is missing. Initiating download...\n", name)
			if err := downloadBinary(name, binDir); err != nil {
				return "", err
			}
		} else {
			fmt.Printf("✔ %s found in local vault.\n", name)
		}
	}

	return binDir, nil
}

func runMinio(ctx context.Context, binDir string) {
	os.MkdirAll("./vault_data", 0755)

	binPath := filepath.Join(binDir, "minio")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	cmd := exec.CommandContext(ctx, binPath, "server", "./vault_data", "--address", ":9000", "--console-address", ":9001")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ MinIO failed: %v\n", err)
	}
}

func runTunnel(ctx context.Context, binDir string) {
	binPath := filepath.Join(binDir, "cloudflared")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	cmd := exec.CommandContext(ctx, binPath, "tunnel", "--url", "http://localhost:9001")

	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Printf("❌ Tunnel pipe error: %v\n", err)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("❌ Tunnel start error: %v\n", err)
		return
	}

	re := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	scanner := bufio.NewScanner(stderr)

	for scanner.Scan() {
		line := scanner.Text()
		if foundURL := re.FindString(line); foundURL != "" {
			fmt.Printf("\n✨ SUCCESS! BareVault is live at: %s\n", foundURL)
		}
	}
}

func downloadBinary(name, targetDir string) error {
	var urls []string

	osName := runtime.GOOS
	arch := runtime.GOARCH

	base := "https://github.com/cloudflare/cloudflared/releases/latest/download/"

	if name == "minio" {
		ext := ""
		if osName == "windows" {
			ext = ".exe"
		}
		urls = append(urls,
			fmt.Sprintf(
				"https://dl.min.io/server/minio/release/%s-%s/%s%s",
				osName,
				arch,
				name,
				ext,
			),
		)
	} else {
	switch osName {
	case "windows":
		urls = append(urls, base+"cloudflared-windows-amd64.exe")

	case "darwin":
		if arch == "arm64" {
			urls = append(urls,
				base+"cloudflared-darwin-arm64.tgz",
			)
		} else {
			urls = append(urls,
				base+"cloudflared-darwin-amd64.tgz",
			)
		}

	case "linux":
		urls = append(urls,
			fmt.Sprintf("%scloudflared-linux-%s", base, arch),
		)
	}
}

	client := &http.Client{}

	var resp *http.Response
	var err error
	var lastStatus string
	success := false

	for _, url := range urls {
		fmt.Printf("\n Attempting download from: %s\n", url)

		resp, err = client.Get(url)
		if err != nil {
			lastStatus = err.Error()
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			success = true
			break
		}

		lastStatus = resp.Status
		resp.Body.Close()
	}

	if !success {
		return fmt.Errorf("all download attempts failed. Last status: %s", lastStatus)
	}

	defer resp.Body.Close()

	if name == "cloudflared" && osName == "darwin" {
	fmt.Printf("📦 Downloading archive (%d MB)...\n", resp.ContentLength/1024/1024)

	archivePath := filepath.Join(targetDir, "cloudflared.tgz")
	out, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer out.Close()

	pw := &ProgressWriter{
		Total:    resp.ContentLength,
		FileName: "cloudflared.tgz",
	}

	_, err = io.Copy(out, io.TeeReader(resp.Body, pw))
	fmt.Println()
	if err != nil {
		return err
	}

	fmt.Println("📂 Extracting archive...")

	cmd := exec.Command("tar", "-xzf", archivePath, "-C", targetDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract archive: %v", err)
	}

	os.Remove(archivePath)

	fmt.Println("🔐 Setting executable permission...")

	return os.Chmod(filepath.Join(targetDir, "cloudflared"), 0755)
}

	fileName := name
	if osName == "windows" {
		fileName += ".exe"
	}

	dest := filepath.Join(targetDir, fileName)
	os.Remove(dest)

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	pw := &ProgressWriter{
		Total:    resp.ContentLength,
		FileName: name,
	}

	_, err = io.Copy(out, io.TeeReader(resp.Body, pw))
	fmt.Println()

	if err != nil {
		return err
	}

	return os.Chmod(dest, 0755)
}