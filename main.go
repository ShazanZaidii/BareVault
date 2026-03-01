package main

import (
    "bufio"
    "bytes"
    "context"
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "runtime"
    "sort"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/atotto/clipboard"
    "github.com/denisbrodbeck/machineid"
    "github.com/minio/madmin-go/v3"
)

// ==========================================
// CORE INFRASTRUCTURE CONFIGURATION
// ==========================================
const (
    CloudflareGatewayURL = "https://YOUR_WORKER_URL_HERE.workers.dev"
    HMACSecret           = "YOUR_SECRET_HMAC_KEY" 
)

var (
    dbMutex        sync.Mutex
    dbPath         string
    hardwareNodeID string
    minioCancel    context.CancelFunc
)

type ProgressWriter struct {
    Total      int64
    Downloaded int64
    FileName   string
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
    n := len(p)
    pw.Downloaded += int64(n)
    if pw.Total > 0 {
        fmt.Printf("\rDownloading %s: %.2f%%", pw.FileName, float64(pw.Downloaded)/float64(pw.Total)*100)
    }
    return n, nil
}

type Invite struct {
    Token   string   `json:"token"`
    MaxUses int      `json:"max_uses"`
    Used    int      `json:"used"`
    Policy  string   `json:"policy"`
    Buckets []string `json:"buckets"` 
    Created string   `json:"created_at"`
}

type Employee struct {
    Name      string `json:"name"`
    AccessKey string `json:"access_key"`
    SecretKey string `json:"secret_key"`
    Role      string `json:"role"`
    AddedAt   string `json:"added_at"`
}

type LocalDB struct {
    Invites   map[string]*Invite  `json:"invites"`
    Employees map[string]Employee `json:"employees"`
}

func generateRandomString(length int) string {
    b := make([]byte, length)
    rand.Read(b)
    return hex.EncodeToString(b)[:length]
}

func main() {
    fmt.Printf("BAREVAULT: Multi-Tenant Engine Starting on %s (%s)...\n", runtime.GOOS, runtime.GOARCH)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    fmt.Println("Step 1: Preparing essential vault components...")
    binDir, err := prepareEnvironment()
    if err != nil {
        log.Fatalf("ERROR: Setup failed: %v", err)
    }
    fmt.Println("Step 1 Complete: System is ready.")

    vaultsDir := filepath.Join(".", "vaults")
    os.MkdirAll(vaultsDir, 0755)

    var vaultName string
    reader := bufio.NewReader(os.Stdin)
    showAllVaults := false

    for vaultName == "" {
        entries, _ := os.ReadDir(vaultsDir)
        
        type vaultInfo struct {
            name    string
            modTime time.Time
        }
        var vaults []vaultInfo

        for _, e := range entries {
            if e.IsDir() {
                info, err := e.Info()
                if err == nil {
                    vaults = append(vaults, vaultInfo{name: e.Name(), modTime: info.ModTime()})
                }
            }
        }

        sort.Slice(vaults, func(i, j int) bool {
            return vaults[i].modTime.After(vaults[j].modTime)
        })

        var existingVaults []string
        for _, v := range vaults {
            existingVaults = append(existingVaults, v.name)
        }

        fmt.Println("\nWorkspace Configuration")
        
        if len(existingVaults) > 0 {
            displayCount := len(existingVaults)
            if !showAllVaults && displayCount > 5 {
                displayCount = 5
            }

            fmt.Println("Available Vaults:")
            for i := 0; i < displayCount; i++ {
                fmt.Printf("  %d) %s\n", i+1, existingVaults[i])
            }

            currentIndex := displayCount + 1
            viewAllIndex := -1
            
            if !showAllVaults && len(existingVaults) > 5 {
                viewAllIndex = currentIndex
                fmt.Printf("-------------------------\n")
                fmt.Printf("  %d) View all %d vaults\n", viewAllIndex, len(existingVaults))
                currentIndex++
            }

            createNewIndex := currentIndex
            fmt.Printf("  %d) Create New Workspace\n", createNewIndex)
            currentIndex++

            deleteIndex := currentIndex
            fmt.Printf("  %d) Delete a Workspace\n", deleteIndex)
            currentIndex++

            exitIndex := currentIndex
            fmt.Printf("-------------------------\n")
            fmt.Printf("  %d) Exit\n", exitIndex)

            fmt.Print("\n> Select option: ")
            choiceStr, _ := reader.ReadString('\n')
            choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))

            if err == nil {
                if choice > 0 && choice <= displayCount {
                    vaultName = existingVaults[choice-1]
                    break
                } else if choice == viewAllIndex {
                    showAllVaults = true
                    continue
                } else if choice == createNewIndex {
                    fmt.Print("> Enter New Vault Name: ")
                    vaultName, _ = reader.ReadString('\n')
                    vaultName = strings.TrimSpace(vaultName)
                    break
                } else if choice == deleteIndex {
                    fmt.Print("> Enter Vault Number or Name to Delete: ")
                    toDelete, _ := reader.ReadString('\n')
                    toDelete = strings.TrimSpace(toDelete)
                    
                    var matchByIndex string
                    var matchByName string

                    if idx, err := strconv.Atoi(toDelete); err == nil && idx > 0 && idx <= len(existingVaults) {
                        matchByIndex = existingVaults[idx-1]
                    }

                    for _, v := range existingVaults {
                        if v == toDelete {
                            matchByName = v
                            break
                        }
                    }

                    var targetVault string

                    if matchByIndex != "" && matchByName != "" && matchByIndex != matchByName {
                        fmt.Printf("\nConflict detected for '%s'. Did you mean:\n", toDelete)
                        fmt.Printf("  1) Vault #%s (Named: '%s')\n", toDelete, matchByIndex)
                        fmt.Printf("  2) Vault Named '%s'\n", matchByName)
                        fmt.Print("> Select 1 or 2: ")
                        
                        resolve, _ := reader.ReadString('\n')
                        resolve = strings.TrimSpace(resolve)
                        
                        if resolve == "1" {
                            targetVault = matchByIndex
                        } else if resolve == "2" {
                            targetVault = matchByName
                        } else {
                            fmt.Println("Invalid selection. Deletion cancelled.")
                            continue
                        }
                    } else if matchByIndex != "" {
                        targetVault = matchByIndex
                    } else if matchByName != "" {
                        targetVault = matchByName
                    }

                    if targetVault != "" {
                        fmt.Printf("WARNING: Type 'yes' to permanently delete vault '%s' and its registry: ", targetVault)
                        confirm, _ := reader.ReadString('\n')
                        if strings.TrimSpace(confirm) == "yes" {
                            os.RemoveAll(filepath.Join(vaultsDir, targetVault))
                            home, _ := os.UserHomeDir()
                            os.RemoveAll(filepath.Join(home, ".barevault", "databases", targetVault))
                            fmt.Printf("Vault '%s' successfully deleted.\n", targetVault)
                            
                            if len(existingVaults)-1 <= 5 {
                                showAllVaults = false
                            }
                        } else {
                            fmt.Println("Deletion cancelled.")
                        }
                    } else {
                        fmt.Println("Vault not found.")
                    }
                    continue
                } else if choice == exitIndex {
                    fmt.Println("Exiting BareVault...")
                    return
                }
            }
            fmt.Println("Invalid choice. Try again.")
        } else {
            fmt.Println("  1) Create New Workspace")
            fmt.Println("  2) Exit")
            fmt.Print("\n> Select option: ")
            
            choiceStr, _ := reader.ReadString('\n')
            choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
            
            if err == nil && choice == 1 {
                fmt.Print("> Enter New Vault Name: ")
                vaultName, _ = reader.ReadString('\n')
                vaultName = strings.TrimSpace(vaultName)
                break
            } else if err == nil && choice == 2 {
                fmt.Println("Exiting BareVault...")
                return
            } else {
                fmt.Println("Invalid choice. Try again.")
            }
        }
    }

    if vaultName == "" {
        vaultName = "Default-Vault"
    }

    vaultDir := filepath.Join(vaultsDir, vaultName)
    os.MkdirAll(vaultDir, 0755)
    currentTime := time.Now().Local()
    os.Chtimes(vaultDir, currentTime, currentTime)
    
    profile := getOrGenerateProfile(vaultDir, vaultName)

    initDB(vaultName)

    apiPort := getFreePort()
    consolePort := getFreePort()
    adminPort := getFreePort()

    go func() {
        scanner := bufio.NewScanner(os.Stdin)
        for scanner.Scan() {
            input := strings.TrimSpace(strings.ToLower(scanner.Text()))
            if input == "q" || input == "quit" || input == "stop" {
                fmt.Println("\n\nInitiating secure shutdown across all tunnels...")
                cancel()
                return
            }
        }
    }()

    hwID, err := machineid.ProtectedID("barevault")
    if err != nil {
        hwID, _ = os.Hostname()
    }

    baseID := hwID
    if len(baseID) > 16 {
        baseID = baseID[:16]
    }
    hardwareNodeID = baseID

    profile.SyncSecret = generateDeterministicHash(hwID + "_lock_secret")[:16]

    vaultHash := generateDeterministicHash(hwID + vaultName)[:6]
    webID := fmt.Sprintf("%s-%s-web", baseID, vaultHash)
    apiID := fmt.Sprintf("%s-%s-api", baseID, vaultHash)
    adminID := fmt.Sprintf("%s-%s-admin", baseID, vaultHash)

    permanentApiURL := fmt.Sprintf("%s/%s", CloudflareGatewayURL, apiID)

    var wg sync.WaitGroup
    fmt.Printf("\nStep 2: Igniting isolated instance [%s]...\n", vaultName)

    minioCtx, cancelMinio := context.WithCancel(ctx)
    minioCancel = cancelMinio

    wg.Add(1)
    go func() {
        defer wg.Done()
        runMinio(minioCtx, binDir, vaultDir, apiPort, consolePort, profile)
    }()

    go startAdminAPI(adminPort, apiPort, profile)

    fmt.Println("\nNegotiating Secure Web Tunnel (1/3)...")
    webURLChan := make(chan string)
    go runTunnel(ctx, binDir, consolePort, "WEB", webURLChan)

    var webTunnelURL string
    select {
    case webTunnelURL = <-webURLChan:
        fmt.Println("   Web Tunnel established!")
    case <-time.After(30 * time.Second):
        log.Fatalf("\nTIMEOUT: Web Tunnel failed to connect. Try running 'killall cloudflared'")
    }

    fmt.Println("   [Sleeping 8 seconds to clear Cloudflare anti-spam monitors...]")
    time.Sleep(8 * time.Second)

    fmt.Println("Negotiating Secure API Tunnel (2/3)...")
    apiURLChan := make(chan string)
    go runTunnel(ctx, binDir, apiPort, "API", apiURLChan)

    var apiTunnelURL string
    select {
    case apiTunnelURL = <-apiURLChan:
        fmt.Println("   API Tunnel established!")
    case <-time.After(30 * time.Second):
        log.Fatalf("\nTIMEOUT: API Tunnel failed to connect.")
    }

    fmt.Println("Negotiating Secure Admin Tunnel (3/3)...")
    adminURLChan := make(chan string)
    go runTunnel(ctx, binDir, adminPort, "ADMIN", adminURLChan)

    var adminTunnelURL string
    select {
    case adminTunnelURL = <-adminURLChan:
        fmt.Println("   Admin Tunnel established!")
    case <-time.After(30 * time.Second):
        log.Fatalf("\nTIMEOUT: Admin Tunnel failed to connect.")
    }

    hardwareSecret := generateDeterministicHash(baseID + "_master_lock")[:16]

    syncURL(webID, webTunnelURL, hardwareSecret)
    syncURL(apiID, apiTunnelURL, hardwareSecret)
    syncURL(adminID, adminTunnelURL, hardwareSecret)

    go func() {
        ticker := time.NewTicker(6 * time.Hour)
        for {
            select {
            case <-ticker.C:
                syncURL(webID, webTunnelURL, hardwareSecret)
            case <-ctx.Done():
                ticker.Stop()
                return
            }
        }
    }()

    clipboard.WriteAll(profile.SecretKey)
    time.Sleep(150 * time.Millisecond)
    clipboard.WriteAll(profile.AccessKey)
    time.Sleep(150 * time.Millisecond)
    clipboard.WriteAll(permanentApiURL)

    var localIP string
    addrs, _ := net.InterfaceAddrs()
    for _, a := range addrs {
        if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
            localIP = ipnet.IP.String()
            break
        }
    }
    if localIP == "" {
        localIP = "127.0.0.1"
    }

    fmt.Printf("\nSUCCESS! [%s] is fully online.\n", profile.VaultName)
    fmt.Println("=========================================================")
    
    fmt.Printf("\033[32m Credentials automatically copied to clipboard! \033[0m\n")
    fmt.Printf("\033[32m (Order: Secret Key, Access Key, Endpoint). Just paste them into the portal. \033[0m\n")
    fmt.Println("=========================================================")
    
    fmt.Printf("BAREVAULT ADMIN PORTAL\n")
    fmt.Printf("   Open this Site and Enter Credentials from below to LogIn as ADMIN\n")
    fmt.Printf("   Portal    : https://shazanzaidii.github.io/barevault-web/\n")
    fmt.Printf("\nADMIN CREDENTIALS (Login Using these as ADMIN)\n")
    fmt.Printf("   Endpoint  : %s\n", permanentApiURL)
    fmt.Printf("   Access Key: %s\n", profile.AccessKey)
    fmt.Printf("   Secret Key: %s\n", profile.SecretKey)
    fmt.Printf("\nADMIN BYPASS (Local Network Legacy Console)\n")
    fmt.Printf("   Dashboard : http://%s:%s\n", localIP, consolePort)
    fmt.Println("=========================================================")
    fmt.Println(" IMPORTANT: DO NOT CLOSE THIS TERMINAL")
    fmt.Println(" SAFE STOP    : Type 'q' and press Enter")
    fmt.Println("=========================================================")

    wg.Wait()
    if ctx.Err() != nil {
        fmt.Printf("[%s] Shutdown Successfully. Vault is offline.\n", profile.VaultName)
    }
}

func getFreePort() string {
    addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
    l, _ := net.ListenTCP("tcp", addr)
    defer l.Close()
    return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}

func prepareEnvironment() (string, error) {
    home, _ := os.UserHomeDir()
    binDir := filepath.Join(home, ".barevault", "bin")
    os.MkdirAll(binDir, 0755)

    required := []string{"cloudflared", "minio"}
    for _, name := range required {
        binaryName := name
        if runtime.GOOS == "windows" {
            binaryName += ".exe"
        }
        path := filepath.Join(binDir, binaryName)
        if _, err := os.Stat(path); os.IsNotExist(err) {
            downloadBinary(name, binDir)
        }
    }
    return binDir, nil
}

func runMinio(ctx context.Context, binDir string, vaultDir string, apiPort string, webPort string, profile VaultProfile) {
    binPath := filepath.Join(binDir, "minio")
    if runtime.GOOS == "windows" {
        binPath += ".exe"
    }
    
    cmd := exec.CommandContext(ctx, binPath, "server", vaultDir, "--address", ":"+apiPort, "--console-address", ":"+webPort)
    
    cmd.Env = append(os.Environ(),
        "MINIO_ROOT_USER="+profile.AccessKey,
        "MINIO_ROOT_PASSWORD="+profile.SecretKey,
        "MINIO_API_CORS_ALLOW_ORIGIN=*",
    )
    cmd.Run()
}

func runTunnel(ctx context.Context, binDir string, port string, tag string, urlChan chan<- string) {
    binPath := filepath.Join(binDir, "cloudflared")
    if runtime.GOOS == "windows" {
        binPath += ".exe"
    }

    cmd := exec.CommandContext(ctx, binPath, "tunnel", "--url", "http://127.0.0.1:"+port)

    stderr, _ := cmd.StderrPipe()
    cmd.Start()

    scanner := bufio.NewScanner(stderr)
    re := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
    urlSent := false

    for scanner.Scan() {
        line := scanner.Text()

        if !urlSent {
            fmt.Printf("   [%s] %s\n", tag, line)
        }

        if foundURL := re.FindString(line); foundURL != "" && !urlSent {
            urlSent = true
            urlChan <- foundURL
        }
    }
}

func syncURL(machineID string, tunnelURL string, syncSecret string) {
    gatewaySyncURL := fmt.Sprintf("%s/api/sync", CloudflareGatewayURL)
    
    payload := fmt.Sprintf(`{"p_machine_name": "%s", "p_current_url": "%s", "p_vault_secret": "%s"}`, machineID, tunnelURL, syncSecret)

    req, _ := http.NewRequest("POST", gatewaySyncURL, bytes.NewBuffer([]byte(payload)))
    req.Header.Add("Content-Type", "application/json")
    
    timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
    
    h := hmac.New(sha256.New, []byte(HMACSecret))
    h.Write([]byte(machineID + timestamp))
    signature := hex.EncodeToString(h.Sum(nil))

    req.Header.Add("X-Timestamp", timestamp)
    req.Header.Add("X-Signature", signature)

    client := &http.Client{}
    resp, err := client.Do(req)

    if err != nil {
        fmt.Println("Warning: Could not connect to global routing network.")
        return
    }
    defer resp.Body.Close()

    if resp.StatusCode == 403 {
        fmt.Println("CRITICAL: Gateway rejected connection (Time out of sync or invalid signature).")
        return
    }

    bodyBytes, _ := io.ReadAll(resp.Body)
    var res map[string]interface{}
    json.Unmarshal(bodyBytes, &res)

    if res["status"] == "error" {
        fmt.Printf("CRITICAL: %s\n", res["message"])
        return
    }

    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        fmt.Printf("Cloud Sync Active: %s is cryptographically locked!\n", machineID)
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
        urls = append(urls, fmt.Sprintf("https://dl.min.io/server/minio/release/%s-%s/%s%s", osName, arch, name, ext))
    } else {
        switch osName {
        case "windows":
            urls = append(urls, base+"cloudflared-windows-amd64.exe")
        case "darwin":
            if arch == "arm64" {
                urls = append(urls, base+"cloudflared-darwin-arm64.tgz")
            } else {
                urls = append(urls, base+"cloudflared-darwin-amd64.tgz")
            }
        case "linux":
            urls = append(urls, fmt.Sprintf("%scloudflared-linux-%s", base, arch))
        }
    }

    client := &http.Client{}
    var resp *http.Response
    var err error
    var lastStatus string
    success := false

    for _, url := range urls {
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
        archivePath := filepath.Join(targetDir, "cloudflared.tgz")
        out, _ := os.Create(archivePath)
        defer out.Close()
        pw := &ProgressWriter{Total: resp.ContentLength, FileName: "cloudflared.tgz"}
        io.Copy(out, io.TeeReader(resp.Body, pw))
        fmt.Println("\nExtracting archive...")
        exec.Command("tar", "-xzf", archivePath, "-C", targetDir).Run()
        os.Remove(archivePath)
        return os.Chmod(filepath.Join(targetDir, "cloudflared"), 0755)
    }

    fileName := name
    if osName == "windows" {
        fileName += ".exe"
    }
    dest := filepath.Join(targetDir, fileName)
    os.Remove(dest)
    out, _ := os.Create(dest)
    defer out.Close()
    pw := &ProgressWriter{Total: resp.ContentLength, FileName: name}
    io.Copy(out, io.TeeReader(resp.Body, pw))
    fmt.Println()
    return os.Chmod(dest, 0755)
}

type VaultProfile struct {
    VaultName  string `json:"vault_name"`
    Hash       string `json:"hash"`
    AccessKey  string `json:"access_key"`
    SecretKey  string `json:"secret_key"`
    SyncSecret string `json:"sync_secret"`
}

func generateDeterministicHash(input string) string {
    h := sha256.New()
    h.Write([]byte(input))
    return hex.EncodeToString(h.Sum(nil))
}

func getOrGenerateProfile(vaultDir string, vaultName string) VaultProfile {
    configPath := filepath.Join(vaultDir, ".profile.json")

    if _, err := os.Stat(configPath); err == nil {
        file, _ := os.ReadFile(configPath)
        var profile VaultProfile
        json.Unmarshal(file, &profile)
        return profile
    }

    hostname, _ := os.Hostname()

    deterministicHash := generateDeterministicHash(hostname + vaultName)[:6]
    deterministicLockKey := generateDeterministicHash(hostname + vaultName + "_lock_secret")[:16]
    deterministicAccessKey := "bv_admin_" + generateDeterministicHash(hostname + vaultName + "_access")[:6]

    newProfile := VaultProfile{
        VaultName:  vaultName,
        Hash:       deterministicHash,
        AccessKey:  deterministicAccessKey,
        SecretKey:  generateDeterministicHash(hostname + vaultName + "_secret")[:32],
        SyncSecret: deterministicLockKey,
    }

    data, _ := json.MarshalIndent(newProfile, "", "  ")
    os.WriteFile(configPath, data, 0600)
    return newProfile
}

func initDB(vaultName string) {
    home, _ := os.UserHomeDir()
    dbDir := filepath.Join(home, ".barevault", "databases", vaultName)
    os.MkdirAll(dbDir, 0755)
    dbPath = filepath.Join(dbDir, "registry.json")

    if _, err := os.Stat(dbPath); os.IsNotExist(err) {
        emptyDB := LocalDB{
            Invites:   make(map[string]*Invite),
            Employees: make(map[string]Employee),
        }
        saveDB(emptyDB)
    }
}

func loadDB() (LocalDB, error) {
    dbMutex.Lock()
    defer dbMutex.Unlock()

    var db LocalDB
    data, err := os.ReadFile(dbPath)
    if err != nil {
        return db, err
    }
    err = json.Unmarshal(data, &db)
    return db, err
}

func saveDB(db LocalDB) error {
    dbMutex.Lock()
    defer dbMutex.Unlock()

    data, err := json.MarshalIndent(db, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(dbPath, data, 0600)
}

func startAdminAPI(port string, apiPort string, profile VaultProfile) {
    mux := http.NewServeMux()

    isAdmin := func(r *http.Request) bool {
        authHeader := r.Header.Get("Authorization")

        if authHeader == "Bearer "+profile.SecretKey {
            return true
        }

        db, err := loadDB()
        if err == nil {
            for _, emp := range db.Employees {
                if authHeader == "Bearer "+emp.SecretKey && emp.Role == "admin" {
                    return true
                }
            }
        }

        return false
    }

    mux.HandleFunc("/api/invite/create", func(w http.ResponseWriter, r *http.Request) {
        if !isAdmin(r) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }

        var req struct {
            MaxUses int      `json:"max_uses"`
            Policy  string   `json:"policy"`
            Buckets []string `json:"buckets"`
        }
        json.NewDecoder(r.Body).Decode(&req)

        if req.MaxUses <= 0 {
            req.MaxUses = 1
        }
        if req.Policy == "" {
            req.Policy = "view_upload_delete" // Changed default
        }

        db, _ := loadDB()
        token := "invite-" + generateRandomString(12)

        db.Invites[token] = &Invite{
            Token:   token,
            MaxUses: req.MaxUses,
            Used:    0,
            Policy:  req.Policy,
            Buckets: req.Buckets,
            Created: time.Now().Format(time.RFC3339),
        }

        saveDB(db)
        fmt.Fprintf(w, `{"status": "success", "invite_token": "%s"}`, token)
    })

    mux.HandleFunc("/api/invite/redeem", func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            Token string `json:"token"`
            Name  string `json:"name"`
        }
        json.NewDecoder(r.Body).Decode(&req)

        db, _ := loadDB()
        invite, exists := db.Invites[req.Token]

        if !exists || invite.Used >= invite.MaxUses {
            http.Error(w, `{"error": "Invalid or expired invite code"}`, http.StatusBadRequest)
            return
        }

        // Generate prefix so the UI knows exactly what buttons to show
        prefix := "emp_"
        if invite.Policy == "admin" {
            prefix = "bv_admin_"
        } else if invite.Policy == "view_upload_delete" {
            prefix = "bv_vud_"
        } else if invite.Policy == "view_upload" {
            prefix = "bv_vu_"
        } else if invite.Policy == "readonly" {
            prefix = "bv_ro_"
        }

        newAccessKey := prefix + generateRandomString(8)
        newSecretKey := generateRandomString(32)

        // ==============================================================
        // STRICT DYNAMIC IAM POLICY GENERATION
        // ==============================================================
        var resourceStr string
        if len(invite.Buckets) > 0 && invite.Policy != "admin" {
            var resArray []string
            for _, b := range invite.Buckets {
                resArray = append(resArray, fmt.Sprintf(`"arn:aws:s3:::%s"`, b))
                resArray = append(resArray, fmt.Sprintf(`"arn:aws:s3:::%s/*"`, b))
            }
            resourceStr = strings.Join(resArray, ",")
        } else {
            resourceStr = `"arn:aws:s3:::*"`
        }

        var iamPolicyString string
        baseActions := `"s3:ListAllMyBuckets","s3:GetBucketLocation"`

        if invite.Policy == "admin" {
            iamPolicyString = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::*"]}]}`
        } else if invite.Policy == "view_upload_delete" {
            iamPolicyString = fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[%s],"Resource":["arn:aws:s3:::*"]},{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket","s3:PutObject","s3:DeleteObject"],"Resource":[%s]}]}`, baseActions, resourceStr)
        } else if invite.Policy == "view_upload" {
            iamPolicyString = fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[%s],"Resource":["arn:aws:s3:::*"]},{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket","s3:PutObject"],"Resource":[%s]}]}`, baseActions, resourceStr)
        } else { // readonly
            iamPolicyString = fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[%s],"Resource":["arn:aws:s3:::*"]},{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":[%s]}]}`, baseActions, resourceStr)
        }
        
        iamPolicy := []byte(iamPolicyString)
        // ==============================================================

        madminClient, err := madmin.New("127.0.0.1:"+apiPort, profile.AccessKey, profile.SecretKey, false)
        if err != nil {
            log.Printf("Madmin Init Error: %v", err)
            http.Error(w, `{"error": "Internal Client Error"}`, http.StatusInternalServerError)
            return
        }

        _, err = madminClient.AddServiceAccount(context.Background(), madmin.AddServiceAccountReq{
            TargetUser: profile.AccessKey,
            AccessKey:  newAccessKey,
            SecretKey:  newSecretKey,
            Policy:     iamPolicy,
        })

        if err != nil {
            log.Printf("MinIO IAM Error: %v", err)
            http.Error(w, `{"error": "Failed to provision MinIO storage keys. Check terminal."}`, http.StatusInternalServerError)
            return
        }

        db.Employees[newAccessKey] = Employee{
            Name:      req.Name,
            AccessKey: newAccessKey,
            SecretKey: newSecretKey,
            Role:      invite.Policy,
            AddedAt:   time.Now().Format(time.RFC3339),
        }

        invite.Used++
        saveDB(db)

        response := fmt.Sprintf(`{"status": "success", "access_key": "%s", "secret_key": "%s"}`, newAccessKey, newSecretKey)
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(response))
    })

    mux.HandleFunc("/api/employees/list", func(w http.ResponseWriter, r *http.Request) {
        if !isAdmin(r) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        db, _ := loadDB()
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(db.Employees)
    })

    mux.HandleFunc("/api/employees/revoke", func(w http.ResponseWriter, r *http.Request) {
        if !isAdmin(r) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        var payload struct {
            AccessKey string `json:"access_key"`
        }
        json.NewDecoder(r.Body).Decode(&payload)

        madminClient, _ := madmin.New("127.0.0.1:"+apiPort, profile.AccessKey, profile.SecretKey, false)

        err := madminClient.DeleteServiceAccount(context.Background(), payload.AccessKey)
        if err != nil {
            log.Printf("Failed to delete service account: %v", err)
            http.Error(w, `{"error": "Failed to delete from MinIO"}`, http.StatusInternalServerError)
            return
        }

        db, _ := loadDB()
        delete(db.Employees, payload.AccessKey)
        saveDB(db)

        w.Write([]byte(`{"status": "success"}`))
    })

    handler := func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        mux.ServeHTTP(w, r)
    }

    fmt.Printf("   Local Admin API active on port %s\n", port)
    http.ListenAndServe("127.0.0.1:"+port, http.HandlerFunc(handler))
}
