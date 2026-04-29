#!/usr/bin/env bash
# Render a Codex skill from a shared base plus an optional repo-local overlay.

set -euo pipefail

usage() {
  cat <<'EOF'
usage: render-codex-skill.sh <skill-name> <base-skill-path> <target-repo>

Renders .codex/skills/<skill-name>/SKILL.md to stdout.
Repo-local additions belong in .codex/skill-overrides/<skill-name>.md.
Write stdout to a temp file, then move it into place; redirecting directly to
the target path creates/truncates the file before safety checks run.

Set MY_SKILL_MIGRATE_CODEX_SKILLS=1 only during a one-time migration. With that
set, a legacy unmarked target may be replaced by generated output. Move any
repo-specific delta to .codex/skill-overrides/<skill-name>.md before migrating.
EOF
}

if [[ "$#" -ne 3 ]]; then
  usage >&2
  exit 3
fi

skill="$1"
base="$2"
repo="$3"
target="$repo/.codex/skills/$skill/SKILL.md"
overlay="$repo/.codex/skill-overrides/$skill.md"

if [[ ! -f "$base" ]]; then
  echo "Missing base skill: $base" >&2
  exit 3
fi

if grep -qx '<!-- my-skill:generated' "$base"; then
  echo "Base skill already contains a my-skill generated marker: $base" >&2
  exit 3
fi

sha_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha_stdin() {
  shasum -a 256 | awk '{print $1}'
}

trim_trailing_blank_lines() {
  awk '
    { lines[NR] = $0 }
    END {
      n = NR
      while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--
      for (i = 1; i <= n; i++) print lines[i]
    }
  ' "$1"
}

trim_edge_blank_lines() {
  awk '
    { lines[NR] = $0 }
    END {
      first = 1
      last = NR
      while (first <= last && lines[first] ~ /^[[:space:]]*$/) first++
      while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
      for (i = first; i <= last; i++) print lines[i]
    }
  ' "$1"
}

has_nonempty_overlay() {
  [[ -f "$overlay" ]] && grep -q '[^[:space:]]' "$overlay"
}

migration_enabled() {
  [[ "${MY_SKILL_MIGRATE_CODEX_SKILLS:-}" == "1" ]]
}

strip_generated_header() {
  local input="$1" output="$2"
  awk '
    $0 == "<!-- my-skill:generated" {
      if (!skipped) {
        in_header = 1
        skipped = 1
        next
      }
    }
    in_header {
      if ($0 == "-->") {
        in_header = 0
      }
      next
    }
    { print }
  ' "$input" > "$output"
}

generated_output_hash() {
  local input="$1"
  awk '
    $0 == "<!-- my-skill:generated" { in_header = 1; next }
    in_header && $0 == "-->" { exit }
    in_header && $1 == "output-sha256:" { print $2; exit }
  ' "$input"
}

emit_with_header() {
  local body="$1" header="$2" close_line
  close_line="$(awk '
    NR == 1 && $0 == "---" { in_frontmatter = 1; next }
    in_frontmatter && $0 == "---" { print NR; exit }
  ' "$body")"

  if [[ -n "$close_line" ]]; then
    sed -n "1,${close_line}p" "$body"
    cat "$header"
    sed -n "$((close_line + 1)),\$p" "$body"
  else
    cat "$header"
    cat "$body"
  fi
}

tmp_body="$(mktemp)"
tmp_header="$(mktemp)"
tmp_current="$(mktemp)"
trap 'rm -f "$tmp_body" "$tmp_header" "$tmp_current"' EXIT

empty_sha="$(printf '' | sha_stdin)"
base_sha="$(sha_file "$base")"
overlay_sha="$empty_sha"

if has_nonempty_overlay; then
  trim_trailing_blank_lines "$base" > "$tmp_body"
  overlay_sha="$(sha_file "$overlay")"
  {
    printf '\n## Repo Overlay\n\n'
    printf 'The following instructions are maintained in `.codex/skill-overrides/%s.md` for this repo.\n\n' "$skill"
    trim_edge_blank_lines "$overlay"
  } >> "$tmp_body"
else
  cp "$base" "$tmp_body"
  if [[ -f "$overlay" ]]; then
    overlay_sha="$(sha_file "$overlay")"
  fi
fi

output_sha="$(sha_file "$tmp_body")"

cat > "$tmp_header" <<EOF
<!-- my-skill:generated
skill: $skill
base-sha256: $base_sha
overlay-sha256: $overlay_sha
output-sha256: $output_sha
do-not-edit: edit .codex/skill-overrides/$skill.md instead
-->
EOF

if [[ -f "$target" ]]; then
  if grep -qx '<!-- my-skill:generated' "$target"; then
    expected_current_sha="$(generated_output_hash "$target")"
    if [[ -z "$expected_current_sha" ]]; then
      echo "Generated marker is missing output-sha256: $target" >&2
      exit 3
    fi
    strip_generated_header "$target" "$tmp_current"
    current_sha="$(sha_file "$tmp_current")"
    if [[ "$current_sha" != "$expected_current_sha" ]]; then
      echo "Generated skill was edited directly: $target" >&2
      echo "Move repo-specific changes to .codex/skill-overrides/$skill.md before deploy." >&2
      exit 1
    fi
  elif ! cmp -s "$target" "$tmp_body"; then
    if migration_enabled; then
      if has_nonempty_overlay; then
        echo "Migrating legacy direct-edited skill with repo override: $target" >&2
      else
        echo "Migrating legacy direct-edited skill without repo override: $target" >&2
      fi
    else
      echo "Legacy direct-edited skill without generated marker: $target" >&2
      if has_nonempty_overlay; then
        echo "Review .codex/skill-overrides/$skill.md, then rerun deploy with --migrate-codex-skills." >&2
      else
        echo "Rerun deploy with --migrate-codex-skills after confirming there is no repo-specific delta to preserve." >&2
      fi
      exit 2
    fi
  fi
fi

emit_with_header "$tmp_body" "$tmp_header"
