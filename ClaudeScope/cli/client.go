package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

var daemonBaseURL = "http://localhost:5812"

// APIError carries the daemon's own error code (e.g. SESSION_NOT_FOUND)
// through to main.go, instead of it being collapsed into a generic
// COMMAND_FAILED at the top level.
type APIError struct {
	Msg  string
	Code string
}

func (e *APIError) Error() string { return e.Msg }

var httpClient = &http.Client{Timeout: 10 * time.Second}

func PingDaemon() bool {
	resp, err := httpClient.Get(daemonBaseURL + "/ping")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func EnsureDaemon() error {
	if PingDaemon() {
		return nil
	}
	if err := spawnDaemon(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}
	delay := 200 * time.Millisecond
	for i := 0; i < 4; i++ {
		time.Sleep(delay)
		if PingDaemon() {
			return nil
		}
		delay *= 2
	}
	return fmt.Errorf("daemon unavailable after start attempt")
}

func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	cmd := exec.Command(exe, "--daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setSysProcAttr(cmd)
	return cmd.Start()
}

func DoRequest(method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, daemonBaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var daemonErr struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.Unmarshal(data, &daemonErr); err == nil && daemonErr.Error != "" {
			return nil, &APIError{Msg: daemonErr.Error, Code: daemonErr.Code}
		}
		return nil, fmt.Errorf("%s", string(data))
	}
	return data, nil
}
