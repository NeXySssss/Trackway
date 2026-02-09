#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="${1:-.}"
cd "${REPO_ROOT}"

echo "[security] running govulncheck"
go run golang.org/x/vuln/cmd/govulncheck@latest ./...

if command -v gitleaks >/dev/null 2>&1; then
  echo "[security] running gitleaks"
  gitleaks detect --no-git --source . --redact --exit-code 1
else
  echo "[security] gitleaks not found, running regex-based secret checks"
  rg -n --hidden \
    -g '!internal/dashboard/frontend/node_modules/**' \
    -g '!.git/**' \
    -- 'AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|AIza[0-9A-Za-z\-_]{35}|xox[baprs]-[0-9A-Za-z-]{10,}|-----BEGIN (RSA|EC|OPENSSH|DSA) PRIVATE KEY-----' . >/tmp/trackway-secret-scan.txt || true
  if [ -s /tmp/trackway-secret-scan.txt ]; then
    echo "[security] potential secrets found:"
    cat /tmp/trackway-secret-scan.txt
    exit 1
  fi
fi

echo "[security] passed"

