package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type secretRule struct {
	name    string
	pattern *regexp.Regexp
}

var rules = []secretRule{
	{
		name:    "aws-access-key",
		pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	},
	{
		name:    "github-token",
		pattern: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	},
	{
		name:    "google-api-key",
		pattern: regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),
	},
	{
		name:    "slack-token",
		pattern: regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	},
	{
		name:    "private-key-header",
		pattern: regexp.MustCompile(`-----BEGIN (RSA|EC|OPENSSH|DSA) PRIVATE KEY-----`),
	},
	{
		name:    "telegram-bot-token",
		pattern: regexp.MustCompile(`\b[0-9]{8,10}:[A-Za-z0-9_-]{20,}\b`),
	},
}

type finding struct {
	file string
	line int
	rule string
}

func main() {
	files, err := trackedFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "secretscan: list tracked files: %v\n", err)
		os.Exit(1)
	}

	findings := make([]finding, 0, 8)
	for _, file := range files {
		fileFindings, err := scanFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "secretscan: scan file %s: %v\n", file, err)
			os.Exit(1)
		}
		findings = append(findings, fileFindings...)
	}

	if len(findings) == 0 {
		fmt.Println("secretscan: no suspicious secrets found in tracked files")
		return
	}

	fmt.Fprintln(os.Stderr, "secretscan: potential secrets found:")
	for _, item := range findings {
		fmt.Fprintf(os.Stderr, "- %s:%d (%s)\n", item.file, item.line, item.rule)
	}
	os.Exit(1)
}

func trackedFiles() ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	files := make([]string, 0, 256)
	for scanner.Scan() {
		path := strings.TrimSpace(scanner.Text())
		if path == "" {
			continue
		}
		if shouldSkip(path) {
			continue
		}
		files = append(files, filepath.Clean(path))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func shouldSkip(path string) bool {
	path = filepath.ToSlash(path)
	if strings.Contains(path, "/node_modules/") {
		return true
	}
	return false
}

func scanFile(path string) ([]finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(data, []byte{0x00}) {
		return nil, nil
	}

	lines := bytes.Split(data, []byte{'\n'})
	findings := make([]finding, 0, 2)
	for i, line := range lines {
		text := string(line)
		for _, rule := range rules {
			if rule.pattern.MatchString(text) {
				findings = append(findings, finding{
					file: path,
					line: i + 1,
					rule: rule.name,
				})
			}
		}
	}
	return findings, nil
}
