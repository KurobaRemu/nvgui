package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultPathProbeTimeout = 1500 * time.Millisecond

var pathProbeCache = struct {
	sync.Mutex
	entries map[string][]string
}{entries: make(map[string][]string)}

type pathProbeResult struct {
	path string
	err  error
}

func resolveLoginPATH(ctx context.Context, env []string, opts PathProbeOptions) ([]string, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseEnv := append([]string(nil), env...)
	basePath := envValue(baseEnv, "PATH")
	if !opts.Force && !needsLoginPATHProbe(basePath) {
		return baseEnv, false
	}
	timeout := defaultPathProbeTimeout
	if opts.Timeout > 0 {
		timeout = time.Duration(opts.Timeout) * time.Millisecond
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	shell := opts.Shell
	if shell == "" {
		shell = envValue(baseEnv, "SHELL")
	}
	if shell == "" {
		shell = "/bin/zsh"
	}
	probe := opts.Probe
	if probe == nil {
		probe = loginShellFallbackProbe([]string{shell, "/bin/zsh", "/bin/sh"})
	}

	cacheKey := shell + "\x00" + basePath
	if !opts.NoCache {
		pathProbeCache.Lock()
		cached, ok := pathProbeCache.entries[cacheKey]
		pathProbeCache.Unlock()
		if ok {
			return mergeLoginPATH(baseEnv, cached), true
		}
	}

	resultCh := make(chan pathProbeResult, 1)
	go func() {
		path, err := probe(probeCtx, shell, append([]string(nil), baseEnv...))
		resultCh <- pathProbeResult{path: path, err: err}
	}()
	var probedPath string
	select {
	case <-probeCtx.Done():
		return baseEnv, false
	case result := <-resultCh:
		if result.err != nil {
			return baseEnv, false
		}
		probedPath = result.path
	}
	entries := splitPATH(probedPath)
	if len(entries) == 0 {
		return baseEnv, false
	}
	if !opts.NoCache {
		pathProbeCache.Lock()
		pathProbeCache.entries[cacheKey] = append([]string(nil), entries...)
		pathProbeCache.Unlock()
	}
	return mergeLoginPATH(baseEnv, entries), true
}

func loginShellFallbackProbe(shells []string) PathProbeFunc {
	return func(ctx context.Context, _ string, baseEnv []string) (string, error) {
		seen := make(map[string]bool, len(shells))
		var lastErr error
		for _, candidate := range shells {
			if candidate == "" || seen[candidate] || ctx.Err() != nil {
				continue
			}
			seen[candidate] = true
			path, err := defaultPathProbe(ctx, candidate, baseEnv)
			if err == nil {
				return path, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = ctx.Err()
		}
		if lastErr == nil {
			lastErr = errors.New("app: no login shell available")
		}
		return "", lastErr
	}
}

func needsLoginPATHProbe(basePath string) bool {
	if basePath == "" || basePath == defaultSystemPATH {
		return true
	}
	if pathHasExecutableNvim(basePath) {
		return false
	}
	for _, directory := range splitPATH(basePath) {
		if directory == "/opt/homebrew/bin" || directory == "/usr/local/bin" {
			return false
		}
	}
	return true
}

func pathHasExecutableNvim(path string) bool {
	for _, directory := range splitPATH(path) {
		if directory == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(directory, "nvim"))
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

func defaultPathProbe(ctx context.Context, shell string, baseEnv []string) (string, error) {
	markerBytes := make([]byte, 16)
	if _, err := rand.Read(markerBytes); err != nil {
		return "", err
	}
	marker := "NVGUI_PATH_" + hex.EncodeToString(markerBytes)
	command := exec.CommandContext(ctx, shell, "-l", "-c", "printf '\\n%s\\n%s\\n' '"+marker+"' \"$PATH\"")
	command.Env = baseEnv
	command.WaitDelay = 200 * time.Millisecond
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(output), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == marker && index+1 < len(lines) {
			return strings.TrimSpace(lines[index+1]), nil
		}
	}
	return "", errors.New("app: login shell PATH marker missing")
}

func splitPATH(path string) []string {
	return filepath.SplitList(path)
}

func mergeLoginPATH(env []string, probed []string) []string {
	entries := splitPATH(envValue(env, "PATH"))
	for _, candidate := range probed {
		if candidate == "" || containsString(entries, candidate) {
			continue
		}
		entries = append(entries, candidate)
	}
	path := strings.Join(entries, string(filepath.ListSeparator))
	merged := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			if !replaced {
				merged = append(merged, "PATH="+path)
				replaced = true
			}
			continue
		}
		merged = append(merged, entry)
	}
	if !replaced {
		merged = append(merged, "PATH="+path)
	}
	return merged
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
