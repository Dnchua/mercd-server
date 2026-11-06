#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
# CLIENT_SOURCE_DIR：android-bootstrap/build.sh 的整包 staging 树
# （decoded-single，资源随包并于首启预置 files/）时指向该目录。
source_dir="$(cd "$script_dir/.." && pwd)/${CLIENT_SOURCE_DIR:-client/decoded}"
source_dir="$(cd "$source_dir" 2>/dev/null && pwd || printf '%s' "$source_dir")"
output_dir="${CLIENT_OUTPUT_DIR:-$repo_root/output/rebuilt}"

command -v python3 >/dev/null 2>&1 || { printf 'client build: python3 required to read client/baseline.json\n' >&2; exit 1; }
baseline_json="$script_dir/baseline.json"
original_apk="$repo_root/$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["originalApk"])' "$baseline_json")"
base_name="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["outputBaseName"])' "$baseline_json")"
unsigned_apk="$output_dir/$base_name-unsigned.apk"
aligned_apk="$output_dir/$base_name-aligned-unsigned.apk"
signed_apk="$output_dir/$base_name.apk"
sign_requested=false

case "${1:-}" in
  "") ;;
  --sign) sign_requested=true ;;
  *) printf 'usage: %s [--sign]\n' "$0" >&2; exit 2 ;;
esac

fail() {
  printf 'client build: %s\n' "$1" >&2
  exit 1
}

test -f "$source_dir/apktool.yml" || fail "missing client/decoded/apktool.yml"
mkdir -p "$output_dir"

orig_apk_name="$(basename "$original_apk")"
expected_apk_hash="$(awk -v f="$orig_apk_name" 'NF >= 2 { line = $0; sub(/^[0-9a-fA-F]+[[:space:]]+/, "", line); if (line == f) {print $1; exit} }' "$repo_root/inputs/apk/SHA256SUMS")"
test -n "$expected_apk_hash" || fail "no SHA256SUMS entry for $orig_apk_name"
actual_apk_hash="$(shasum -a 256 "$original_apk" | awk '{print $1}')"
test "$actual_apk_hash" = "$expected_apk_hash" || fail "original APK SHA-256 mismatch"

if test -n "${APKTOOL_JAR:-}"; then
  test -f "$APKTOOL_JAR" || fail "APKTOOL_JAR does not exist"
  apktool_version="$(java -jar "$APKTOOL_JAR" --version)"
  apktool_build() { java -jar "$APKTOOL_JAR" b "$source_dir" -o "$unsigned_apk"; }
elif command -v apktool >/dev/null 2>&1; then
  apktool_version="$(apktool --version)"
  apktool_build() { apktool b "$source_dir" -o "$unsigned_apk"; }
else
  fail "set APKTOOL_JAR or install apktool on PATH"
fi

test "$apktool_version" = "3.0.3" || fail "apktool 3.0.3 required; found $apktool_version"

android_sdk="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-$HOME/Library/Android/sdk}}"
if test -n "${ZIPALIGN:-}"; then
  zipalign_bin="$ZIPALIGN"
else
  zipalign_bin="$(find "$android_sdk/build-tools" -type f -name zipalign 2>/dev/null | sort -V | tail -1)"
fi
test -x "$zipalign_bin" || fail "zipalign not found; set ZIPALIGN or ANDROID_SDK_ROOT"

apktool_build
"$zipalign_bin" -f 4 "$unsigned_apk" "$aligned_apk"
"$zipalign_bin" -c 4 "$aligned_apk" >/dev/null
unzip -tq "$aligned_apk" >/dev/null

final_apk="$aligned_apk"
if test "$sign_requested" = true; then
  : "${ANDROID_KEYSTORE:?ANDROID_KEYSTORE is required with --sign}"
  : "${ANDROID_KEY_ALIAS:?ANDROID_KEY_ALIAS is required with --sign}"
  : "${ANDROID_KEYSTORE_PASS:?ANDROID_KEYSTORE_PASS is required with --sign}"
  : "${ANDROID_KEY_PASS:?ANDROID_KEY_PASS is required with --sign}"

  if test -n "${APKSIGNER:-}"; then
    apksigner_bin="$APKSIGNER"
  else
    apksigner_bin="$(find "$android_sdk/build-tools" -type f -name apksigner 2>/dev/null | sort -V | tail -1)"
  fi
  test -x "$apksigner_bin" || fail "apksigner not found; set APKSIGNER or ANDROID_SDK_ROOT"

  "$apksigner_bin" sign \
    --ks "$ANDROID_KEYSTORE" \
    --ks-key-alias "$ANDROID_KEY_ALIAS" \
    --ks-pass env:ANDROID_KEYSTORE_PASS \
    --key-pass env:ANDROID_KEY_PASS \
    --v1-signing-enabled true \
    --v2-signing-enabled true \
    --v3-signing-enabled false \
    --out "$signed_apk" "$aligned_apk"
  "$apksigner_bin" verify --verbose --print-certs "$signed_apk" > "$output_dir/$base_name.apksigner.txt"
  final_apk="$signed_apk"
fi

final_hash="$(shasum -a 256 "$final_apk" | awk '{print $1}')"
printf '%s  %s\n' "$final_hash" "$(basename "$final_apk")" > "$final_apk.sha256"
source_commit="$(git -C "$repo_root" rev-parse --verify HEAD 2>/dev/null || printf unknown)"
if test -n "$(git -C "$repo_root" status --porcelain --untracked-files=all -- client)"; then
  source_dirty=true
else
  source_dirty=false
fi
printf '{\n  "schemaVersion": 1,\n  "sourceTree": "client/decoded",\n  "sourceCommit": "%s",\n  "sourceDirty": %s,\n  "baseApkSha256": "%s",\n  "apktoolVersion": "%s",\n  "signed": %s,\n  "output": "%s",\n  "outputSha256": "%s"\n}\n' \
  "$source_commit" "$source_dirty" "$actual_apk_hash" "$apktool_version" "$sign_requested" "$(basename "$final_apk")" "$final_hash" \
  > "$output_dir/client-build.json"

printf 'client build: %s\nsha256: %s\n' "$final_apk" "$final_hash"
