package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type otaCommandPayload struct {
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
}

type otaAvailablePayload struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	Url     string `json:"url"`
	Notes   string `json:"notes,omitempty"`
}

type otaStatusPayload struct {
	State    string `json:"state"`
	Progress int    `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`
}

var (
	otaCheckOnce sync.Once
	otaExecMu    sync.Mutex
	otaRunning   bool
)

func findGitRepoDir() string {
	if envDir := strings.TrimSpace(os.Getenv("PHEV2MQTT_REPO_DIR")); envDir != "" {
		if _, err := os.Stat(filepath.Join(envDir, ".git")); err == nil {
			return envDir
		}
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 4; i++ {
			candidates = append(candidates, dir)
			dir = filepath.Dir(dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for i := 0; i < 4; i++ {
			candidates = append(candidates, dir)
			dir = filepath.Dir(dir)
		}
	}

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if _, err2 := os.Stat(filepath.Join(dir, "go.mod")); err2 == nil {
				return dir
			}
		}
	}
	return ""
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func (m *mqttClient) publishOtaStatus(state string, progress int, message string) {
	if m.client == nil || !m.client.IsConnected() {
		return
	}
	b, err := json.Marshal(otaStatusPayload{
		State:    state,
		Progress: progress,
		Message:  message,
	})
	if err != nil {
		return
	}
	m.client.Publish(m.topic("/ota/status"), 1, true, string(b))
}

func (m *mqttClient) initOtaOnConnect() {
	repoDir := findGitRepoDir()
	if repoDir == "" {
		log.Debug("OTA: git repo directory not found, skipping Pi git OTA")
		return
	}

	if ver, err := runGit(repoDir, "rev-parse", "--short", "HEAD"); err == nil && ver != "" {
		m.client.Publish(m.topic("/ota/current_version"), 1, true, ver)
	}
	m.publishOtaStatus("idle", 0, "")

	go m.checkGitUpdate()

	otaCheckOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if m.client != nil && m.client.IsConnected() {
					m.checkGitUpdate()
				}
			}
		}()
	})
}

func (m *mqttClient) checkGitUpdate() {
	if m.client == nil || !m.client.IsConnected() {
		return
	}
	repoDir := findGitRepoDir()
	if repoDir == "" {
		return
	}

	if _, err := runGit(repoDir, "fetch", "--quiet"); err != nil {
		log.Debugf("OTA git fetch failed: %v", err)
		return
	}

	localVer, err := runGit(repoDir, "rev-parse", "--short", "HEAD")
	if err != nil || localVer == "" {
		return
	}
	m.client.Publish(m.topic("/ota/current_version"), 1, true, localVer)

	remoteRef := "@{u}"
	remoteVer, err := runGit(repoDir, "rev-parse", "--short", remoteRef)
	if err != nil || remoteVer == "" {
		for _, fallback := range []string{"origin/master", "origin/main"} {
			if v, err2 := runGit(repoDir, "rev-parse", "--short", fallback); err2 == nil && v != "" {
				remoteRef = fallback
				remoteVer = v
				break
			}
		}
	}
	if remoteVer == "" {
		return
	}

	if remoteVer == localVer {
		m.client.Publish(m.topic("/ota/available"), 1, true, "")
		return
	}

	notes, _ := runGit(repoDir, "log", "-1", "--pretty=%s", remoteRef)
	b, err := json.Marshal(otaAvailablePayload{
		Version: remoteVer,
		Mode:    "git",
		Url:     "git://" + remoteRef,
		Notes:   notes,
	})
	if err != nil {
		return
	}
	log.Infof("OTA: Pi git update available %s -> %s (%s)", localVer, remoteVer, notes)
	m.client.Publish(m.topic("/ota/available"), 1, true, string(b))
}

func (m *mqttClient) handleOtaCommand(payloadStr string) {
	var cmdPayload otaCommandPayload
	if err := json.Unmarshal([]byte(payloadStr), &cmdPayload); err != nil {
		log.Warnf("OTA: invalid ota/command JSON: %v", err)
		return
	}

	switch strings.ToLower(strings.TrimSpace(cmdPayload.Action)) {
	case "check":
		go m.checkGitUpdate()
	case "start":
		otaExecMu.Lock()
		if otaRunning {
			otaExecMu.Unlock()
			return
		}
		otaRunning = true
		otaExecMu.Unlock()
		go m.performPiGitOta()
	}
}

func (m *mqttClient) performPiGitOta() {
	defer func() {
		otaExecMu.Lock()
		otaRunning = false
		otaExecMu.Unlock()
	}()

	repoDir := findGitRepoDir()
	if repoDir == "" {
		m.publishOtaStatus("error", 0, "Git repo directory not found")
		return
	}

	m.publishOtaStatus("downloading", 15, "")
	if _, err := runGit(repoDir, "pull", "--ff-only"); err != nil {
		log.Errorf("OTA git pull failed: %v", err)
		m.publishOtaStatus("error", 0, "git pull failed: "+err.Error())
		return
	}

	m.publishOtaStatus("flashing", 55, "")
	newBin := filepath.Join(repoDir, "phev2mqtt.new")
	buildCmd := exec.Command("go", "build", "-o", newBin, ".")
	buildCmd.Dir = repoDir
	var buildErr bytes.Buffer
	buildCmd.Stderr = &buildErr
	if err := buildCmd.Run(); err != nil {
		_ = os.Remove(newBin)
		msg := strings.TrimSpace(buildErr.String())
		if msg == "" {
			msg = err.Error()
		}
		log.Errorf("OTA go build failed: %s", msg)
		m.publishOtaStatus("error", 0, "go build failed: "+msg)
		return
	}

	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		exePath = filepath.Join(repoDir, "phev2mqtt")
	}

	if err := exec.Command("mv", "-f", newBin, exePath).Run(); err != nil {
		if err2 := exec.Command("sudo", "-n", "mv", "-f", newBin, exePath).Run(); err2 != nil {
			_ = os.Remove(newBin)
			log.Errorf("OTA binary replace failed: %v / %v", err, err2)
			m.publishOtaStatus("error", 0, "binary replace failed")
			return
		}
	}

	m.publishOtaStatus("rebooting", 100, "")
	m.client.Publish(m.topic("/ota/available"), 1, true, "")

	restartCmd := strings.TrimSpace(os.Getenv("PHEV2MQTT_RESTART_CMD"))
	if restartCmd == "" {
		svcName := strings.TrimSpace(os.Getenv("PHEV2MQTT_SERVICE_NAME"))
		if svcName == "" {
			svcName = "phev2mqtt"
		}
		restartCmd = fmt.Sprintf("sleep 1 && (sudo -n systemctl restart %s || systemctl restart %s)", svcName, svcName)
	}

	log.Infof("OTA: Pi update built successfully, restarting service via: %s", restartCmd)
	if err := exec.Command("sh", "-c", restartCmd).Start(); err != nil {
		log.Errorf("OTA service restart failed: %v", err)
		m.publishOtaStatus("error", 0, "service restart failed: "+err.Error())
	}
}
