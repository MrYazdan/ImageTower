#!/usr/bin/env bash
set -euo pipefail

# scripts/generate-changelog.sh
# Generates CHANGELOG.md from Git history using git-cliff (or fallback git log parser)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

if command -v git-cliff &>/dev/null; then
    echo "Running git-cliff with cliff.toml..."
    git-cliff --config cliff.toml --output CHANGELOG.md
    echo "CHANGELOG.md successfully generated with git-cliff!"
else
    echo "git-cliff is not installed locally. Generating standard changelog from git log..."
    OUTPUT_FILE="${ROOT_DIR}/CHANGELOG.md"
    
    cat << 'EOF' > "${OUTPUT_FILE}"
# Changelog

All notable changes to this project are documented in this file.

EOF
    
    echo "## Recent Changes" >> "${OUTPUT_FILE}"
    echo "" >> "${OUTPUT_FILE}"
    
    git log --pretty=format:"- %s (%h)" >> "${OUTPUT_FILE}"
    echo "" >> "${OUTPUT_FILE}"
    
    echo "Generated fallback CHANGELOG.md using git log."
    echo "Tip: Install git-cliff (cargo install git-cliff) or let GitHub Actions build it automatically on tag push!"
fi
